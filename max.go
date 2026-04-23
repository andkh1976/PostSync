package main

import (
	"context"
	// "errors" // DEPRECATED (Sprint 4 Correction): использовался только в forwardMaxToTg
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	maxbot "github.com/max-messenger/max-bot-api-client-go"
	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

func (b *Bridge) listenMax(ctx context.Context) {
	var updates <-chan maxschemes.UpdateInterface

	if b.cfg.WebhookURL != "" {
		whPath := b.maxWebhookPath()
		whURL := strings.TrimRight(b.cfg.WebhookURL, "/") + whPath
		ch := make(chan maxschemes.UpdateInterface, 100)
		http.HandleFunc(whPath, b.maxApi.GetHandler(ch))
		updateTypes := []string{
			"message_created", "message_edited", "message_removed",
			"message_callback", "bot_added", "bot_removed",
			"user_added", "user_removed", "chat_title_changed",
		}
		if _, err := b.maxApi.Subscriptions.Subscribe(ctx, whURL, updateTypes, ""); err != nil {
			slog.Error("MAX webhook subscribe failed", "err", err)
			return
		}
		updates = ch
		slog.Info("MAX webhook mode")
	} else {
		updates = b.maxApi.GetUpdates(ctx)
		slog.Info("MAX polling mode")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case upd, ok := <-updates:
			if !ok {
				return
			}

			slog.Debug("MAX update", "type", fmt.Sprintf("%T", upd))

			// Бот добавлен в чат — запоминаем chat ID
			if addUpd, isAdd := upd.(*maxschemes.BotAddedToChatUpdate); isAdd {
				go b.onMaxBotAdded(ctx, addUpd.ChatId)
				continue
			}

			// Бот удалён из чата — можно оставить запись (она не мешает)
			if rmUpd, isRm := upd.(*maxschemes.BotRemovedFromChatUpdate); isRm {
				slog.Info("MAX bot removed from chat", "chatID", rmUpd.ChatId)
				continue
			}

			// Изменение заголовка чата — обновляем в БД
			if titleUpd, isTitle := upd.(*maxschemes.ChatTitleChangedUpdate); isTitle {
				b.repo.UpsertMaxKnownChat(MaxKnownChat{
					ChatID: titleUpd.ChatId,
					Title:  titleUpd.Title,
				})
				continue
			}

			// Обработка удаления — удаление в MAX не синхронизируется в Telegram (отключено)
			if _, isDel := upd.(*maxschemes.MessageRemovedUpdate); isDel {
				continue
			}

			// Обработка edit — редактирование в MAX не синхронизируется в Telegram (отключено, связь односторонняя TG→MAX)
			if _, isEdit := upd.(*maxschemes.MessageEditedUpdate); isEdit {
				continue
			}

			// Обработка inline-кнопок (crosspost management)
			if cbUpd, isCb := upd.(*maxschemes.MessageCallbackUpdate); isCb {
				b.handleMaxCallback(ctx, cbUpd)
				continue
			}

			msgUpd, isMsg := upd.(*maxschemes.MessageCreatedUpdate)
			if !isMsg {
				continue
			}

			body := msgUpd.Message.Body
			chatID := msgUpd.Message.Recipient.ChatId
			text := strings.TrimSpace(body.Text)
			isDialog := msgUpd.Message.Recipient.ChatType == "dialog"

			slog.Debug("MAX msg received", "uid", msgUpd.Message.Sender.UserId, "chat", chatID, "type", msgUpd.Message.Recipient.ChatType)

			// Запоминаем юзера при личном сообщении
			if isDialog && msgUpd.Message.Sender.UserId != 0 {
				b.repo.TouchUser(msgUpd.Message.Sender.UserId, "max", msgUpd.Message.Sender.Username, msgUpd.Message.Sender.Name)
			}

			if text == "/whoami" {
				m := maxbot.NewMessage().SetChat(chatID).SetText(
					"PostSynk — сервис пересылки постов из Telegram в MAX.\n" +
						"Исходники: https://github.com/andkh1976/PostSync\n" +
						"Лицензия: CC BY-NC 4.0")
				b.maxApi.Messages.Send(ctx, m)
				continue
			}

			if text == "/start" || text == "/help" {
				helpText := "PostSync — пересылка постов из Telegram в MAX.\n\n" +
					"Как начать:\n" +
					"1. Добавьте TG-бота и MAX-бота администраторами в свои каналы (с правом публикации)\n" +
					"2. В боте Telegram нажмите кнопку «Панель управления»\n" +
					"3. В разделе «Связки каналов» выберите TG-канал и MAX-канал для создания связки\n\n" +
					"Всё управление (связки, синхронизация, автозамены) — через Панель управления.\n\n" +
					"Поддержка: https://github.com/andkh1976/PostSync/issues"
				// DEPRECATED (Final Correction): старый текст с ручными командами /bridge, /crosspost
				// helpText := "Бот-мост между MAX и Telegram.\n\n" +
				//      "Команды (группы):\n" +
				//      "/bridge — создать ключ для связки чатов\n" +
				//      "/bridge <ключ> — связать этот чат с Telegram-чатом по ключу\n" +
				//      "/bridge prefix on/off — включить/выключить префикс [TG]/[MAX]\n" +
				//      "/unbridge — удалить связку\n\n" +
				//      "Кросспостинг каналов (в личке бота):\n" +
				//      "/crosspost <TG_ID> — связать MAX-канал с TG-каналом\n" +
				//      "   (TG ID получить: перешлите пост из TG-канала TG-боту)\n\n" +
				//      "Как связать каналы:\n" +
				//      "1. Добавьте бота админом в оба канала (с правом постинга)\n" +
				//      "   TG: " + b.cfg.TgBotURL + "\n" +
				//      "2. Перешлите пост из TG-канала в личку TG-бота\n" +
				//      "3. Бот покажет ID канала — скопируйте\n" +
				//      "4. Здесь в личке напишите: /crosspost <TG_ID>\n" +
				//      "5. Перешлите пост из MAX-канала сюда → готово!\n\n" +
				//      "/crosspost — список всех связок с кнопками управления\n" +
				//      "Управление: перешлите пост из связанного канала → кнопки\n\n" +
				//      "Как связать группы:\n" +
				//      "1. Добавьте бота в оба чата\n" +
				//      "   MAX: " + b.cfg.MaxBotURL + "\n" +
				//      "2. В одном из чатов отправьте /bridge\n" +
				//      "3. Бот выдаст ключ — отправьте его в другом чате\n" +
				//      "4. Готово!\n\n" +
				//      "Поддержка: https://github.com/BEARlogin/max-telegram-bridge-bot/issues"
				// Управление только через TG Mini App — в MAX ссылку не показываем
				helpText += "\n\nКоманды MAX-бота:\n/chatid — узнать ID этого чата\n/confirm <CODE> <MAX_CHAT_ID> — подтвердить владение MAX-каналом"
				m := maxbot.NewMessage().SetChat(chatID).SetText(helpText)
				b.maxApi.Messages.Send(ctx, m)
				continue
			}

			// /chatid — сообщает ID текущего чата (нужно для настройки связки)
			if text == "/chatid" {
				m := maxbot.NewMessage().SetChat(chatID).SetText(
					fmt.Sprintf("ID этого чата: %d\n\nИспользуйте его как «ID MAX-канала» в панели управления.", chatID))
				b.maxApi.Messages.Send(ctx, m)
				continue
			}

			if isDialog && strings.HasPrefix(strings.ToLower(text), "/confirm") {
				code, targetChatID, ok := parseMaxChannelConfirmationCommand(text)
				if !ok {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Неверная команда. Используйте:\n/confirm MAX-XXXXXX <ID_MAX_КАНАЛА>")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}

				confirmation, err := b.repo.GetMaxChannelConfirmationByCode(code)
				if err != nil {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Код подтверждения не найден или уже недействителен.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}
				if confirmation.Status != MaxChannelConfirmationStatusPending {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Этот код уже использован или больше недействителен. Запросите новый код в панели управления.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}
				if time.Now().UTC().After(confirmation.ExpiresAt) {
					b.repo.ExpireMaxChannelConfirmations(time.Now().UTC())
					m := maxbot.NewMessage().SetChat(chatID).SetText("Срок действия кода истёк. Запросите новый код в панели управления.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}

				admins, err := b.maxApi.Chats.GetChatAdmins(ctx, targetChatID)
				if err != nil {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Не удалось проверить канал. Убедитесь, что бот добавлен администратором в MAX-канал и ID указан верно.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}
				if msgUpd.Message.Sender.UserId == 0 || !isMaxUserAdmin(admins.Members, msgUpd.Message.Sender.UserId) {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Вы не являетесь администратором указанного MAX-канала.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}

				confirmed, err := b.repo.MarkMaxChannelConfirmationConfirmed(code, targetChatID, time.Now().UTC())
				if err != nil || confirmed == nil || confirmed.Status != MaxChannelConfirmationStatusConfirmed {
					m := maxbot.NewMessage().SetChat(chatID).SetText("Не удалось подтвердить код. Попробуйте запросить новый код в панели управления.")
					b.maxApi.Messages.Send(ctx, m)
					continue
				}

				m := maxbot.NewMessage().SetChat(chatID).SetText(fmt.Sprintf("Подтверждение сохранено ✅\n\nMAX-канал %d подтверждён. Вернитесь в Telegram Mini App и снова нажмите «Создать связку».", targetChatID))
				b.maxApi.Messages.Send(ctx, m)
				continue
			}

			// DEPRECATED (Sprint 4 Correction): проверка прав админа не нужна — legacy команды отключены
			// isGroup := isMaxGroup(msgUpd.Message.Recipient.ChatType)
			// isAdmin := false
			// if isGroup && msgUpd.Message.Sender.UserId != 0 {
			//      admins, err := b.maxApi.Chats.GetChatAdmins(ctx, chatID)
			//      if err == nil {
			//              isAdmin = isMaxUserAdmin(admins.Members, msgUpd.Message.Sender.UserId)
			//      }
			// } else if isGroup {
			//      isAdmin = true
			// }

			// DEPRECATED (Sprint 4 Correction): /bridge prefix — legacy команда, управление через Mini App
			// if text == "/bridge prefix on" || text == "/bridge prefix off" {
			//      if isGroup && !isAdmin {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Эта команда доступна только админам группы.")
			//              b.maxApi.Messages.Send(ctx, m)
			//              continue
			//      }
			//      on := text == "/bridge prefix on"
			//      if b.repo.SetPrefix("max", chatID, on) {
			//              reply := "Префикс [TG]/[MAX] включён."
			//              if !on {
			//                      reply = "Префикс [TG]/[MAX] выключен."
			//              }
			//              m := maxbot.NewMessage().SetChat(chatID).SetText(reply)
			//              b.maxApi.Messages.Send(ctx, m)
			//      } else {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Чат не связан. Сначала выполните /bridge.")
			//              b.maxApi.Messages.Send(ctx, m)
			//      }
			//      continue
			// }

			// DEPRECATED (Sprint 4 Correction): /bridge — legacy команда, управление через Mini App
			// if text == "/bridge" || strings.HasPrefix(text, "/bridge ") {
			//      if isGroup && !isAdmin {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Эта команда доступна только админам группы.")
			//              b.maxApi.Messages.Send(ctx, m)
			//              continue
			//      }
			//      key := strings.TrimSpace(strings.TrimPrefix(text, "/bridge"))
			//      paired, generatedKey, err := b.repo.Register(key, "max", chatID)
			//      if err != nil {
			//              slog.Error("register failed", "err", err)
			//              continue
			//      }
			//      if paired {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Связано! Сообщения теперь пересылаются.")
			//              b.maxApi.Messages.Send(ctx, m)
			//              slog.Info("paired", "platform", "max", "chat", chatID, "key", key)
			//      } else if generatedKey != "" {
			//              m := maxbot.NewMessage().SetChat(chatID).
			//                      SetText(fmt.Sprintf("Ключ для связки: %s\n\nОтправьте в Telegram-чате:\n/bridge %s\n\nTG-бот: %s", generatedKey, generatedKey, b.cfg.TgBotURL))
			//              b.maxApi.Messages.Send(ctx, m)
			//              slog.Info("pending", "platform", "max", "chat", chatID, "key", generatedKey)
			//      } else {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Ключ не найден или чат той же платформы.")
			//              b.maxApi.Messages.Send(ctx, m)
			//      }
			//      continue
			// }

			// DEPRECATED (Sprint 4 Correction): /unbridge — legacy команда, управление через Mini App
			// if text == "/unbridge" {
			//      if isGroup && !isAdmin {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Эта команда доступна только админам группы.")
			//              b.maxApi.Messages.Send(ctx, m)
			//              continue
			//      }
			//      if b.repo.Unpair("max", chatID) {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Связка удалена.")
			//              b.maxApi.Messages.Send(ctx, m)
			//      } else {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Этот чат не связан.")
			//              b.maxApi.Messages.Send(ctx, m)
			//      }
			//      continue
			// }

			// Обработка ввода замены (если юзер в режиме ожидания)
			if isDialog && !strings.HasPrefix(text, "/") && msgUpd.Message.Sender.UserId != 0 {
				if w, ok := b.getReplWait(msgUpd.Message.Sender.UserId); ok {
					b.clearReplWait(msgUpd.Message.Sender.UserId)
					rule, valid := parseReplacementInput(text)
					if !valid {
						m := maxbot.NewMessage().SetChat(chatID).SetText("Неверный формат. Используйте:\nfrom | to\nили\n/regex/ | to")
						b.maxApi.Messages.Send(ctx, m)
						continue
					}
					rule.Target = w.target
					repl := b.repo.GetCrosspostReplacements(w.maxChatID)
					if w.direction == "tg>max" {
						repl.TgToMax = append(repl.TgToMax, rule)
					} else {
						repl.MaxToTg = append(repl.MaxToTg, rule)
					}
					if err := b.repo.SetCrosspostReplacements(w.maxChatID, repl); err != nil {
						slog.Error("save replacements failed", "err", err)
						m := maxbot.NewMessage().SetChat(chatID).SetText("Ошибка сохранения.")
						b.maxApi.Messages.Send(ctx, m)
						continue
					}
					ruleType := "строка"
					if rule.Regex {
						ruleType = "regex"
					}
					dirLabel := "TG → MAX"
					if w.direction == "max>tg" {
						dirLabel = "MAX → TG"
					}
					m := maxbot.NewMessage().SetChat(chatID).SetText(
						fmt.Sprintf("Замена добавлена (%s, %s):\n%s → %s", dirLabel, ruleType, rule.From, rule.To))
					b.maxApi.Messages.Send(ctx, m)
					continue
				}
			}

			// DEPRECATED (Sprint 4 Correction): /crosspost команда — настройка перенесена в Mini App (POST /api/channels/pair)
			// === Crosspost команды (только в личке бота) ===
			// if isDialog && strings.HasPrefix(text, "/crosspost") {
			//      arg := strings.TrimSpace(strings.TrimPrefix(text, "/crosspost"))
			//      if arg == "" {
			//              links := b.repo.ListCrossposts(msgUpd.Message.Sender.UserId)
			//              if len(links) == 0 {
			//                      noLinksText := "Нет активных связок.\n\nНастройка:\n1. Перешлите пост из TG-канала в личку TG-бота\n   " + b.cfg.TgBotURL + "\n2. Бот покажет ID канала\n3. Здесь напишите: /crosspost <TG_ID>\n4. Перешлите пост из MAX-канала сюда"
			//                      if b.cfg.MiniAppURL != "" {
			//                              noLinksText += "\n\n⚙️ Панель управления: " + b.cfg.MiniAppURL
			//                      }
			//                      m := maxbot.NewMessage().SetChat(chatID).SetText(noLinksText)
			//                      b.maxApi.Messages.Send(ctx, m)
			//              } else {
			//                      for _, l := range links {
			//                              tgTitle := b.tgChatTitle(l.TgChatID)
			//                              statusText := maxCrosspostStatusText(l.TgChatID, l.Direction)
			//                              if tgTitle != "" {
			//                                      statusText = fmt.Sprintf("TG: «%s» (%d)\n", tgTitle, l.TgChatID) + statusText
			//                              }
			//                              if b.cfg.MiniAppURL != "" {
			//                                      statusText += "\n\n⚙️ Управление: " + b.cfg.MiniAppURL
			//                              }
			//                              m := maxbot.NewMessage().SetChat(chatID).SetText(statusText)
			//                              b.maxApi.Messages.Send(ctx, m)
			//                      }
			//              }
			//              continue
			//      }
			//      tgChannelID, err := strconv.ParseInt(arg, 10, 64)
			//      if err != nil {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Неверный ID. Пример: /crosspost -1001234567890")
			//              b.maxApi.Messages.Send(ctx, m)
			//              continue
			//      }
			//      b.cpWaitMu.Lock()
			//      b.cpWait[msgUpd.Message.Sender.UserId] = tgChannelID
			//      b.cpWaitMu.Unlock()
			//      m := maxbot.NewMessage().SetChat(chatID).SetText(
			//              fmt.Sprintf("TG канал ID: %d\n\nТеперь перешлите любой пост из MAX-канала, который хотите связать.", tgChannelID))
			//      b.maxApi.Messages.Send(ctx, m)
			//      slog.Info("crosspost waiting for forward", "user", msgUpd.Message.Sender.UserId, "tgChannel", tgChannelID)
			//      continue
			// }

			// DEPRECATED (Sprint 4 Correction): ручной паринг через пересланное сообщение заменён POST /api/channels/pair
			// if isDialog && msgUpd.Message.Link != nil && msgUpd.Message.Link.Type == maxschemes.FORWARD {
			//      maxChannelID := msgUpd.Message.Link.ChatId
			//      userId := msgUpd.Message.Sender.UserId
			//      b.cpWaitMu.Lock()
			//      tgChannelID, waiting := b.cpWait[userId]
			//      if waiting {
			//              delete(b.cpWait, userId)
			//      }
			//      b.cpWaitMu.Unlock()
			//      if waiting && maxChannelID != 0 {
			//              if _, _, ok := b.repo.GetCrosspostTgChat(maxChannelID, 0); ok {
			//                      m := maxbot.NewMessage().SetChat(chatID).SetText("Этот MAX-канал уже связан.")
			//                      b.maxApi.Messages.Send(ctx, m)
			//                      continue
			//              }
			//              b.cpTgOwnerMu.Lock()
			//              tgOwnerID := b.cpTgOwner[tgChannelID]
			//              b.cpTgOwnerMu.Unlock()
			//              if err := b.repo.PairCrosspost(tgChannelID, maxChannelID, msgUpd.Message.Sender.UserId, tgOwnerID); err != nil {
			//                      slog.Error("crosspost pair failed", "err", err)
			//                      m := maxbot.NewMessage().SetChat(chatID).SetText("Ошибка при создании связки.")
			//                      b.maxApi.Messages.Send(ctx, m)
			//                      continue
			//              }
			//              skb := b.maxApi.Messages.NewKeyboardBuilder()
			//              skb.AddRow().AddCallback("⚙️ Настройки", maxschemes.DEFAULT, "settings")
			//              m := maxbot.NewMessage().SetChat(chatID).
			//                      SetText(fmt.Sprintf("Кросспостинг настроен!\nTG: %d ↔ MAX: %d\nНаправление: ⟷ оба", tgChannelID, maxChannelID)).
			//                      AddKeyboard(skb)
			//              b.maxApi.Messages.Send(ctx, m)
			//              slog.Info("crosspost paired", "tg", tgChannelID, "max", maxChannelID, "maxOwner", msgUpd.Message.Sender.UserId, "tgOwner", tgOwnerID)
			//              continue
			//      }
			//      if maxChannelID != 0 {
			//              if tgID, direction, ok := b.repo.GetCrosspostTgChat(maxChannelID, 0); ok {
			//                      skb := b.maxApi.Messages.NewKeyboardBuilder()
			//                      skb.AddRow().AddCallback("⚙️ Настройки", maxschemes.DEFAULT, "settings")
			//                      m := maxbot.NewMessage().SetChat(chatID).
			//                              SetText(maxCrosspostStatusText(tgID, direction)).
			//                              AddKeyboard(skb)
			//                      b.maxApi.Messages.Send(ctx, m)
			//                      continue
			//              }
			//      }
			//      if maxChannelID != 0 {
			//              m := maxbot.NewMessage().SetChat(chatID).SetText("Этот канал не связан с кросспостингом.\n\nДля настройки:\n/crosspost <TG_ID>")
			//              b.maxApi.Messages.Send(ctx, m)
			//      }
			//      continue
			// }

			// --- MAX→TG forwarding disabled (managed via Mini App) ---
			// // Пересылка (bridge)
			// tgChatID, linked := b.repo.GetTgChat(chatID)
			// if linked && !msgUpd.Message.Sender.IsBot {
			//      // Anti-loop
			//      if !strings.HasPrefix(text, "[TG]") && !strings.HasPrefix(text, "[MAX]") {
			//              prefix := b.repo.HasPrefix("max", chatID)
			//              caption := formatMaxCaption(msgUpd, prefix, b.cfg.MessageNewline)
			//              go b.forwardMaxToTg(ctx, msgUpd, tgChatID, caption)
			//      }
			//      continue
			// }
			//
			// // Пересылка (crosspost fallback)
			// if msgUpd.Message.Sender.IsBot {
			//      continue
			// }
			// tgChatID, direction, cpLinked := b.repo.GetCrosspostTgChat(chatID)
			// if !cpLinked {
			//      continue
			// }
			// if direction == "tg>max" {
			//      continue // только TG→MAX, пропускаем
			// }
			//
			// // Anti-loop
			// if strings.HasPrefix(text, "[TG]") || strings.HasPrefix(text, "[MAX]") {
			//      continue
			// }
			//
			// caption := formatMaxCrosspostCaption(msgUpd)
			//
			// // Применяем замены для MAX→TG
			// repl := b.repo.GetCrosspostReplacements(chatID)
			// if len(repl.MaxToTg) > 0 {
			//      caption = applyReplacements(caption, repl.MaxToTg)
			// }
			//
			// go b.forwardMaxToTg(ctx, msgUpd, tgChatID, caption)
			// --- конец отключённого блока MAX→TG ---
		}
	}
}

// handleMaxCallback обрабатывает нажатия inline-кнопок (crosspost management).
func (b *Bridge) handleMaxCallback(ctx context.Context, cbUpd *maxschemes.MessageCallbackUpdate) {
	data := cbUpd.Callback.Payload
	callbackID := cbUpd.Callback.CallbackID
	userID := cbUpd.Callback.User.UserId

	slog.Debug("MAX callback", "uid", userID, "data", data)

	// cpd:dir:maxChatID — change direction
	if strings.HasPrefix(data, "cpd:") {
		parts := strings.SplitN(data, ":", 3)
		if len(parts) != 3 {
			return
		}
		dir := parts[1]
		maxChatID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return
		}
		if dir != "tg>max" && dir != "max>tg" && dir != "both" {
			return
		}
		if !b.isCrosspostOwner(maxChatID, userID) {
			b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
				Notification: "Только владелец связки может изменять настройки.",
			})
			return
		}
		b.repo.SetCrosspostDirection(maxChatID, dir)

		tgID, _, _ := b.repo.GetCrosspostTgChat(maxChatID, 0)
		skb := b.maxApi.Messages.NewKeyboardBuilder()
		skb.AddRow().AddCallback("⚙️ Настройки", maxschemes.DEFAULT, "settings")
		body := &maxschemes.NewMessageBody{
			Text:        maxCrosspostStatusText(tgID, dir),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(skb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message:      body,
			Notification: "Готово",
		})
		return
	}

	// cpu:maxChatID — unlink (show confirmation)
	if strings.HasPrefix(data, "cpu:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cpu:"), 10, 64)
		if err != nil {
			return
		}
		if !b.isCrosspostOwner(maxChatID, userID) {
			b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
				Notification: "Только владелец связки может удалять.",
			})
			return
		}
		kb := b.maxApi.Messages.NewKeyboardBuilder()
		kb.AddRow().
			AddCallback("Да, удалить", maxschemes.NEGATIVE, fmt.Sprintf("cpuc:%d", maxChatID)).
			AddCallback("Отмена", maxschemes.DEFAULT, fmt.Sprintf("cpux:%d", maxChatID))
		body := &maxschemes.NewMessageBody{
			Text:        "Удалить кросспостинг?",
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(kb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message: body,
		})
		return
	}

	// cpuc:maxChatID — unlink confirmed
	if strings.HasPrefix(data, "cpuc:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cpuc:"), 10, 64)
		if err != nil {
			return
		}
		if !b.isCrosspostOwner(maxChatID, userID) {
			b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
				Notification: "Только владелец связки может удалять.",
			})
			return
		}
		slog.Info("MAX crosspost unlink", "maxChatID", maxChatID, "by", userID)
		b.repo.UnpairCrosspost(maxChatID, userID)
		body := &maxschemes.NewMessageBody{Text: "Кросспостинг удалён."}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message:      body,
			Notification: "Удалено",
		})
		return
	}

	// cpr:maxChatID — show replacements
	if strings.HasPrefix(data, "cpr:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cpr:"), 10, 64)
		if err != nil {
			return
		}
		repl := b.repo.GetCrosspostReplacements(maxChatID)
		id := strconv.FormatInt(maxChatID, 10)
		// Заголовок с кнопками добавления
		kb := maxReplacementsKeyboard(b.maxApi, maxChatID)
		body := &maxschemes.NewMessageBody{
			Text:        formatReplacementsHeader(repl),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(kb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{Message: body})
		// Каждая замена — отдельное сообщение с кнопками
		for i, r := range repl.TgToMax {
			dkb := maxReplItemKeyboard(b.maxApi, "tg>max", i, id, r.Target)
			m := maxbot.NewMessage().SetChat(cbUpd.Callback.User.UserId).
				SetText(formatReplacementItem(r, "tg>max")).
				AddKeyboard(dkb)
			b.maxApi.Messages.Send(ctx, m)
		}
		for i, r := range repl.MaxToTg {
			dkb := maxReplItemKeyboard(b.maxApi, "max>tg", i, id, r.Target)
			m := maxbot.NewMessage().SetChat(cbUpd.Callback.User.UserId).
				SetText(formatReplacementItem(r, "max>tg")).
				AddKeyboard(dkb)
			b.maxApi.Messages.Send(ctx, m)
		}
		return
	}

	// cprt:dir:index:target:maxChatID — toggle replacement target
	if strings.HasPrefix(data, "cprt:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "cprt:"), ":", 4)
		if len(parts) != 4 {
			return
		}
		dir := parts[0]
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			return
		}
		newTarget := parts[2]
		maxChatID, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return
		}
		repl := b.repo.GetCrosspostReplacements(maxChatID)
		id := strconv.FormatInt(maxChatID, 10)
		var r *Replacement
		if dir == "tg>max" && idx < len(repl.TgToMax) {
			r = &repl.TgToMax[idx]
		} else if dir == "max>tg" && idx < len(repl.MaxToTg) {
			r = &repl.MaxToTg[idx]
		}
		if r == nil {
			return
		}
		r.Target = newTarget
		b.repo.SetCrosspostReplacements(maxChatID, repl)
		newText := formatReplacementItem(*r, dir)
		dkb := maxReplItemKeyboard(b.maxApi, dir, idx, id, r.Target)
		body := &maxschemes.NewMessageBody{
			Text:        newText,
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(dkb.Build())},
		}
		label := "весь текст"
		if newTarget == "links" {
			label = "только ссылки"
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message:      body,
			Notification: "Тип: " + label,
		})
		return
	}

	// cprd:dir:index:maxChatID — delete single replacement
	if strings.HasPrefix(data, "cprd:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "cprd:"), ":", 3)
		if len(parts) != 3 {
			return
		}
		dir := parts[0]
		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			return
		}
		maxChatID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return
		}
		repl := b.repo.GetCrosspostReplacements(maxChatID)
		if dir == "tg>max" && idx < len(repl.TgToMax) {
			repl.TgToMax = append(repl.TgToMax[:idx], repl.TgToMax[idx+1:]...)
		} else if dir == "max>tg" && idx < len(repl.MaxToTg) {
			repl.MaxToTg = append(repl.MaxToTg[:idx], repl.MaxToTg[idx+1:]...)
		}
		b.repo.SetCrosspostReplacements(maxChatID, repl)
		body := &maxschemes.NewMessageBody{Text: "Замена удалена."}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message:      body,
			Notification: "Удалено",
		})
		return
	}

	// cpra:dir:maxChatID — choose target (all or links)
	if strings.HasPrefix(data, "cpra:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "cpra:"), ":", 2)
		if len(parts) != 2 {
			return
		}
		dir := parts[0]
		id := parts[1]
		dirLabel := "TG → MAX"
		if dir == "max>tg" {
			dirLabel = "MAX → TG"
		}
		kb := b.maxApi.Messages.NewKeyboardBuilder()
		kb.AddRow().
			AddCallback("📝 Весь текст", maxschemes.DEFAULT, "cprat:"+dir+":all:"+id).
			AddCallback("🔗 Только ссылки", maxschemes.DEFAULT, "cprat:"+dir+":links:"+id)
		body := &maxschemes.NewMessageBody{
			Text:        fmt.Sprintf("Добавление замены для %s.\nГде применять замену?", dirLabel),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(kb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{Message: body})
		return
	}

	// cprat:dir:target:maxChatID — set wait state with target
	if strings.HasPrefix(data, "cprat:") {
		parts := strings.SplitN(strings.TrimPrefix(data, "cprat:"), ":", 3)
		if len(parts) != 3 {
			return
		}
		dir := parts[0]
		target := parts[1]
		maxChatID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return
		}
		b.setReplWait(userID, maxChatID, dir, target)
		body := &maxschemes.NewMessageBody{
			Text: "Отправьте правило замены:\nfrom | to\n\nДля регулярного выражения:\n/regex/ | to\n\nНапример:\nutm_source=tg | utm_source=max",
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{Message: body})
		return
	}

	// cprc:maxChatID — clear all replacements
	if strings.HasPrefix(data, "cprc:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cprc:"), 10, 64)
		if err != nil {
			return
		}
		b.repo.SetCrosspostReplacements(maxChatID, CrosspostReplacements{})
		repl := b.repo.GetCrosspostReplacements(maxChatID)
		kb := maxReplacementsKeyboard(b.maxApi, maxChatID)
		body := &maxschemes.NewMessageBody{
			Text:        formatReplacementsHeader(repl),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(kb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message:      body,
			Notification: "Очищено",
		})
		return
	}

	// cprb:maxChatID — back to crosspost management
	if strings.HasPrefix(data, "cprb:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cprb:"), 10, 64)
		if err != nil {
			return
		}
		tgID, direction, ok := b.repo.GetCrosspostTgChat(maxChatID, 0)
		if !ok {
			return
		}
		skb := b.maxApi.Messages.NewKeyboardBuilder()
		skb.AddRow().AddCallback("⚙️ Настройки", maxschemes.DEFAULT, "settings")
		body := &maxschemes.NewMessageBody{
			Text:        maxCrosspostStatusText(tgID, direction),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(skb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{Message: body})
		return
	}

	// cpux:maxChatID — cancel (return to management keyboard)
	if strings.HasPrefix(data, "cpux:") {
		maxChatID, err := strconv.ParseInt(strings.TrimPrefix(data, "cpux:"), 10, 64)
		if err != nil {
			return
		}
		tgID, direction, ok := b.repo.GetCrosspostTgChat(maxChatID, 0)
		if !ok {
			body := &maxschemes.NewMessageBody{Text: "Кросспостинг не найден."}
			b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
				Message: body,
			})
			return
		}
		skb := b.maxApi.Messages.NewKeyboardBuilder()
		skb.AddRow().AddCallback("⚙️ Настройки", maxschemes.DEFAULT, "settings")
		body := &maxschemes.NewMessageBody{
			Text:        maxCrosspostStatusText(tgID, direction),
			Attachments: []interface{}{maxschemes.NewInlineKeyboardAttachmentRequest(skb.Build())},
		}
		b.maxApi.Messages.AnswerOnCallback(ctx, callbackID, &maxschemes.CallbackAnswer{
			Message: body,
		})
		return
	}
}

// maxCrosspostStatusText возвращает текст статуса кросспостинга для MAX.
func maxCrosspostStatusText(tgChatID int64, direction string) string {
	dirLabel := "⟷ оба"
	switch direction {
	case "tg>max":
		dirLabel = "TG → MAX"
	case "max>tg":
		dirLabel = "MAX → TG"
	}
	return fmt.Sprintf("Кросспостинг настроен\nTG: %d ↔ MAX\nНаправление: %s", tgChatID, dirLabel)
}

// DEPRECATED (Sprint 4 Correction): forwardMaxToTg и все её вызовы закомментированы — MAX→TG пересылка отключена
// // forwardMaxToTg пересылает MAX-сообщение (текст/медиа) в TG-чат.
// func (b *Bridge) forwardMaxToTg(ctx context.Context, msgUpd *maxschemes.MessageCreatedUpdate, tgChatID int64, caption string) {
//         if b.cbBlocked(tgChatID) {
//                 return
//         }
//
//         body := msgUpd.Message.Body
//         chatID := msgUpd.Message.Recipient.ChatId
//         text := strings.TrimSpace(body.Text)
//
//         // Reply ID
//         var replyToID int
//         if body.ReplyTo != "" {
//                 if _, rid, ok := b.repo.LookupTgMsgID(body.ReplyTo); ok {
//                         replyToID = rid
//                 }
//         } else if msgUpd.Message.Link != nil {
//                 mid := msgUpd.Message.Link.Message.Mid
//                 if mid != "" {
//                         if _, rid, ok := b.repo.LookupTgMsgID(mid); ok {
//                                 replyToID = rid
//                         }
//                 }
//         }
//
//         // Проверяем вложения
//         var sent tgbotapi.Message
//         var sendErr error
//         mediaSent := false
//         var qAttType, qAttURL string // для очереди при ошибке
//
//         // Определяем HTML caption если есть markups (для кросспостинга)
//         htmlCaption := caption
//         useHTML := len(body.Markups) > 0 && caption == text
//         if useHTML {
//                 htmlCaption = maxMarkupsToHTML(text, body.Markups)
//         }
//
//         // Собираем вложения: фото/видео → albumMedia (отправляем вместе), остальные → soloMedia
//         var albumMedia []interface{}
//         var soloMedia []struct {
//                 url     string
//                 attType string
//                 name    string
//         }
//         pm := ""
//         if useHTML {
//                 pm = "HTML"
//         }
//
//         for _, att := range body.Attachments {
//                 switch a := att.(type) {
//                 case *maxschemes.PhotoAttachment:
//                         if a.Payload.Url != "" {
//                                 if len(albumMedia) == 0 {
//                                         qAttType, qAttURL = "photo", a.Payload.Url
//                                 }
//                                 p := tgbotapi.NewInputMediaPhoto(tgbotapi.FileURL(a.Payload.Url))
//                                 albumMedia = append(albumMedia, p)
//                         }
//                 case *maxschemes.VideoAttachment:
//                         if a.Payload.Url != "" {
//                                 if len(albumMedia) == 0 {
//                                         qAttType, qAttURL = "video", a.Payload.Url
//                                 }
//                                 v := tgbotapi.NewInputMediaVideo(tgbotapi.FileURL(a.Payload.Url))
//                                 albumMedia = append(albumMedia, v)
//                         }
//                 case *maxschemes.AudioAttachment:
//                         if a.Payload.Url != "" {
//                                 if qAttType == "" {
//                                         qAttType, qAttURL = "audio", a.Payload.Url
//                                 }
//                                 soloMedia = append(soloMedia, struct {
//                                         url     string
//                                         attType string
//                                         name    string
//                                 }{a.Payload.Url, "audio", ""})
//                         }
//                 case *maxschemes.FileAttachment:
//                         if a.Payload.Url != "" {
//                                 if qAttType == "" {
//                                         qAttType, qAttURL = "file", a.Payload.Url
//                                 }
//                                 soloMedia = append(soloMedia, struct {
//                                         url     string
//                                         attType string
//                                         name    string
//                                 }{a.Payload.Url, "file", a.Filename})
//                         }
//                 case *maxschemes.StickerAttachment:
//                         if a.Payload.Url != "" {
//                                 if qAttType == "" {
//                                         qAttType, qAttURL = "sticker", a.Payload.Url
//                                 }
//                                 soloMedia = append(soloMedia, struct {
//                                         url     string
//                                         attType string
//                                         name    string
//                                 }{a.Payload.Url, "sticker", ""})
//                         }
//                 }
//         }
//
//         // Отправляем фото/видео как альбом (если их несколько — grouped, иначе — single)
//         if len(albumMedia) > 0 {
//                 mediaSent = true
//                 // Caption и reply только к первому элементу
//                 if htmlCaption != "" || replyToID != 0 {
//                         switch first := albumMedia[0].(type) {
//                         case tgbotapi.InputMediaPhoto:
//                                 first.Caption = htmlCaption
//                                 if pm != "" {
//                                         first.ParseMode = pm
//                                 }
//                                 albumMedia[0] = first
//                         case tgbotapi.InputMediaVideo:
//                                 first.Caption = htmlCaption
//                                 if pm != "" {
//                                         first.ParseMode = pm
//                                 }
//                                 albumMedia[0] = first
//                         }
//                 }
//
//                 if len(albumMedia) == 1 {
//                         // Одно вложение — отправляем обычным сообщением (альбом из 1 элемента не имеет reply)
//                         sent, sendErr = b.sendTgMediaFromURL(tgChatID, qAttURL, qAttType, htmlCaption, pm, replyToID, b.cfg.maxMaxFileBytes())
//                         var e *ErrFileTooLarge
//                         if errors.As(sendErr, &e) {
//                                 slog.Warn("MAX→TG media too big", "name", e.Name, "size", e.Size)
//                                 m := maxbot.NewMessage().SetChat(chatID).SetText(
//                                         fmt.Sprintf("⚠️ Файл \"%s\" слишком большой для пересылки (%s). Максимальный размер файла %d МБ.",
//                                                 e.Name, formatFileSize(int(e.Size)), b.cfg.MaxMaxFileSizeMB))
//                                 b.maxApi.Messages.Send(ctx, m)
//                         }
//                 } else {
//                         // Несколько — отправляем как media group (альбом)
//                         cfg := tgbotapi.NewMediaGroup(tgChatID, albumMedia)
//                         if replyToID != 0 {
//                                 cfg.ReplyToMessageID = replyToID
//                         }
//                         msgs, err := b.tgBot.SendMediaGroup(cfg)
//                         if err != nil {
//                                 slog.Error("MAX→TG album send failed", "err", err)
//                                 sendErr = err
//                                 m := maxbot.NewMessage().SetChat(chatID).SetText("Не удалось отправить медиаальбом в Telegram.")
//                                 b.maxApi.Messages.Send(ctx, m)
//                         } else if len(msgs) > 0 {
//                                 sent = msgs[0]
//                         }
//                 }
//         }
//
//         // Отправляем остальные вложения (аудио, файлы, стикеры) по одному
//         // Если фото/видео не отправлялось, caption добавляем к первому вложению
//         firstSolo := true
//         for _, sm := range soloMedia {
//                 smCaption := ""
//                 smReplyTo := 0
//                 if firstSolo && !mediaSent {
//                         smCaption = htmlCaption
//                         smReplyTo = replyToID
//                 }
//                 firstSolo = false
//                 s, err := b.sendTgMediaFromURL(tgChatID, sm.url, sm.attType, smCaption, pm, smReplyTo, b.cfg.maxMaxFileBytes(), sm.name)
//                 if err != nil {
//                         var e *ErrFileTooLarge
//                         if errors.As(err, &e) {
//                                 slog.Warn("MAX→TG solo media too big", "name", e.Name, "size", e.Size)
//                                 m := maxbot.NewMessage().SetChat(chatID).SetText(
//                                         fmt.Sprintf("⚠️ Файл \"%s\" слишком большой для пересылки (%s). Максимальный размер файла %d МБ.",
//                                                 e.Name, formatFileSize(int(e.Size)), b.cfg.MaxMaxFileSizeMB))
//                                 b.maxApi.Messages.Send(ctx, m)
//                         } else {
//                                 slog.Error("MAX→TG solo media send failed", "type", sm.attType, "err", err)
//                                 m := maxbot.NewMessage().SetChat(chatID).SetText(
//                                         fmt.Sprintf("Не удалось отправить файл \"%s\" в Telegram.", sm.name))
//                                 b.maxApi.Messages.Send(ctx, m)
//                         }
//                         if sendErr == nil {
//                                 sendErr = err
//                         }
//                 } else if !mediaSent {
//                         sent = s
//                         mediaSent = true
//                 }
//         }
//
//         // Текст без медиа
//         if !mediaSent {
//                 if text == "" {
//                         return
//                 }
//                 // Если есть markups и caption = оригинальный текст (кросспостинг), конвертируем в HTML
//                 if len(body.Markups) > 0 && caption == text {
//                         htmlText := maxMarkupsToHTML(text, body.Markups)
//                         tgMsg := tgbotapi.NewMessage(tgChatID, htmlText)
//                         tgMsg.ParseMode = "HTML"
//                         tgMsg.ReplyToMessageID = replyToID
//                         sent, sendErr = b.tgBot.Send(tgMsg)
//                 } else {
//                         tgMsg := tgbotapi.NewMessage(tgChatID, caption)
//                         tgMsg.ReplyToMessageID = replyToID
//                         sent, sendErr = b.tgBot.Send(tgMsg)
//                 }
//         }
//
//         if sendErr != nil {
//                 slog.Error("MAX→TG send failed", "err", sendErr, "uid", msgUpd.Message.Sender.UserId, "maxChat", chatID, "tgChat", tgChatID)
//                 parseMode := ""
//                 if useHTML {
//                         parseMode = "HTML"
//                 }
//                 // ErrFileTooLarge уже уведомил пользователя выше — не дублируем
//                 var eTooLarge *ErrFileTooLarge
//                 if !errors.As(sendErr, &eTooLarge) {
//                         notifyText := "Не удалось переслать сообщение в Telegram. Попробуем ещё раз автоматически."
//                         if b.cbBlocked(tgChatID) {
//                                 notifyText = "TG API недоступен. Сообщения в очереди, будут доставлены автоматически."
//                         }
//                         m := maxbot.NewMessage().SetChat(chatID).SetText(notifyText)
//                         b.maxApi.Messages.Send(ctx, m)
//                 }
//                 b.enqueueMax2Tg(chatID, tgChatID, body.Mid, htmlCaption, qAttType, qAttURL, parseMode)
//                 b.cbFail(tgChatID)
//         } else {
//                 b.cbSuccess(tgChatID)
//                 slog.Info("MAX→TG sent", "msgID", sent.MessageID, "media", mediaSent, "uid", msgUpd.Message.Sender.UserId, "maxChat", chatID, "tgChat", tgChatID)
//                 b.repo.SaveMsg(tgChatID, sent.MessageID, chatID, body.Mid)
//         }
// }

// onMaxBotAdded вызывается когда бот добавлен в MAX-чат.
// Получает информацию о чате через MAX API и сохраняет её в БД.
func (b *Bridge) onMaxBotAdded(ctx context.Context, chatID int64) {
	chat, err := b.maxApi.Chats.GetChat(ctx, chatID)
	title := ""
	chatType := ""
	if err == nil && chat != nil {
		title = chat.Title
		chatType = string(chat.Type)
	}
	b.repo.UpsertMaxKnownChat(MaxKnownChat{ChatID: chatID, Title: title, ChatType: chatType})
	slog.Info("MAX bot added to chat", "chatID", chatID, "title", title, "type", chatType)

	msg := fmt.Sprintf("Привет! Я PostSync — бот пересылки постов из Telegram.\n\nID этого чата: %d\n\nСкопируйте этот ID и используйте его как «ID MAX-канала» в панели управления бота Telegram.", chatID)
	m := maxbot.NewMessage().SetChat(chatID).SetText(msg)
	b.maxApi.Messages.Send(ctx, m)
}

// CheckMaxAdmin проверяет, является ли пользователь (по ID) администратором в указанном MAX-канале/группе.
// Возвращает false если пользователь не админ, или ошибку если бот не имеет доступа.
func (b *Bridge) CheckMaxAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	admins, err := b.maxApi.Chats.GetChatAdmins(ctx, chatID)
	if err != nil {
		return false, err
	}

	for _, member := range admins.Members {
		if member.UserId == userID {
			return true, nil
		}
	}

	return false, nil
}

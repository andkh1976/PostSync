package main

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var maxChannelConfirmationCodeRe = regexp.MustCompile(`^MAX-[A-F0-9]{6}$`)

func buildMaxChannelConfirmationCode() string {
	const alphabet = "0123456789ABCDEF"
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "MAX-000000"
	}
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return "MAX-" + string(out)
}

func normalizeMaxChannelConfirmationCode(code string) (string, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !maxChannelConfirmationCodeRe.MatchString(code) {
		return "", false
	}
	return code, true
}

func parseMaxChannelConfirmationCommand(text string) (string, int64, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) != 3 || strings.ToLower(parts[0]) != "/confirm" {
		return "", 0, false
	}
	code, ok := normalizeMaxChannelConfirmationCode(parts[1])
	if !ok {
		return "", 0, false
	}
	chatID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || chatID == 0 {
		return "", 0, false
	}
	return code, chatID, true
}

func buildMaxChannelConfirmationInstructions(code string) string {
	return fmt.Sprintf("Чтобы подтвердить владение MAX-каналом, откройте личку с MAX-ботом и отправьте команду:\n\n/confirm %s <ID_MAX_КАНАЛА>\n\nПример:\n/confirm %s 123456\n\nУзнать ID канала можно командой /chatid внутри MAX-канала, где бот уже добавлен администратором.", code, code)
}

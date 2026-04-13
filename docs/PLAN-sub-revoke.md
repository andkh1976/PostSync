# Plan: Admin Command /sub_revoke

## Decision Rationale
Between overloading `/sub_grant` to accept `0` or negative values vs creating a dedicated `/sub_revoke` command, the **dedicated `/sub_revoke` command** is significantly more productive and safe.
- **Safety**: Typing `/sub_grant 12345 0` by accident (e.g., missing a trailing zero for `30`) would instantly kill a user's subscription. A dedicated `/sub_revoke` requires explicit intent.
- **Clarity**: It separates the logic of "extending" from "terminating".
- **Code Cleanliness**: `GrantSubscription` can safely assume additions, while a new `RevokeSubscription` explicitly halts access.

## Implementation Plan

### 1. Database Layer (`repository.go` or `postgres.go`)
- Create a new method: `func (r *Repo) RevokeSubscription(userID int64) error`
- This method will set `subscription_end = NOW()` (or equivalent in the past) and `has_subscription = false` for the given `user_id`.

### 2. Telegram Bot Command (`telegram.go`)
- Inside `listenTelegram`, under the admin block (`if msg.From.ID == b.cfg.AdminChatID`), add a new condition for `/sub_revoke`.
- Logic:
  - Parse the command: `/sub_revoke <ID>`.
  - Validate that exactly one argument (ID) is provided.
  - Call `b.repo.RevokeSubscription(uid)`.
  - Send success message to admin: `"✅ Подписка аннулирована для пользователя <ID>."`
  - (Optional) Send a notification to the user: `"⚠️ Ваш доступ был приостановлен администратором."`

## Phased Rollout
1. **Database-Architect**: Add the repository method.
2. **Backend-Specialist**: Add the handler in `telegram.go`.
3. **Test-Engineer**: Add/run test cases or static analysis.

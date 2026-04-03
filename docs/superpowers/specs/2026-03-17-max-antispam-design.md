# MAX Antispam Bot — Design Spec

## Overview

Antispam bot for MAX messenger with two-tier moderation: fast local filters + AI classification (premium). Configurable through bot DM with inline menus.

## Architecture

Two-level moderation pipeline:

1. **Fast local filter** — stopwords, links, flood detection, regex patterns. Processes 100% of messages, catches ~95% of spam without AI.
2. **AI classifier (premium)** — messages flagged as suspicious by local filter are sent to OpenRouter for classification. Categories: spam, ads, insults, nsfw, scam + custom admin prompts.

## Components

### New Member Verification
- On join: inline button with timeout (default 60s, configurable)
- No click within timeout -> kick
- Clicked -> unmuted, but first 5 messages go through strict filter (links, forwards blocked)

### Message Filtering
- Stopwords (configurable per chat)
- Regex patterns
- Links from new members
- Flood detection: >N identical messages or >M messages in T seconds
- Unicode abuse detection (invisible chars, homoglyphs)

### Suspicion Scoring
Each rule adds points: stopword +3, link from new user +5, flood +4, unicode abuse +2. Threshold configurable. Above threshold -> delete (free) or send to AI (premium).

### AI Moderation (Premium, OpenRouter)
- Receives only suspicious messages (score > threshold)
- Classifies: spam, ads, insult, nsfw, scam, clean
- Admin can add custom prompt ("ban crypto talk", "delete profanity")
- Results cached via fuzzy hash for similar messages

### Punishment Escalation
Configurable chain per chat:
- 1st violation -> delete message
- 2nd -> mute 1 hour
- 3rd -> ban
Defaults are sensible, admin customizes via bot DM.

### Configuration via Bot DM
- `/start` -> list chats where bot is admin
- Select chat -> inline menu with modules (toggle captcha, filters, AI, thresholds)
- `/premium <key>` -> activate premium

### Limits
- Free: up to 3 chats, all filters except AI
- Premium: unlimited chats + AI moderation + custom prompts + violation analytics

## Storage

SQLite default / PostgreSQL optional (like bridge):
- `chats` — chat settings (modules, thresholds, punishment chains)
- `users` — user state (verified, violation_count, muted_until)
- `violations` — violation log (for premium analytics)
- `premium` — keys and subscriptions
- `stopwords` — stopwords per chat

## Stack

- Go, MAX Bot API, SQLite/PostgreSQL
- OpenRouter API for AI
- systemd + deploy.sh
- License: CC BY-NC 4.0

## Project Location

`/home/bearlogin/development/bearlogin/max-antispam`

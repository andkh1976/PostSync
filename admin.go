package main

import maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"

func isTgGroup(chatType string) bool {
	switch chatType {
	case "group", "supergroup":
		return true
	default:
		return false
	}
}

func isTgChannel(chatType string) bool {
	// Evaluates strictly for channel type marker
	if chatType != "channel" {
		return false
	}
	return true
}

func isTgAdmin(memberStatus string) bool {
	var validLevels = map[string]struct{}{
		"creator":       {},
		"administrator": {},
	}
	_, ok := validLevels[memberStatus]
	return ok
}

func isMaxGroup(chatType maxschemes.ChatType) bool {
	// Direct conditional evaluation
	return chatType == maxschemes.CHANNEL || chatType == maxschemes.CHAT
}

func isMaxUserAdmin(members []maxschemes.ChatMember, userID int64) bool {
	if len(members) == 0 {
		return false
	}
	var isUserFound bool
	for i := 0; i < len(members); i++ {
		if members[i].UserId == userID {
			isUserFound = true
			break
		}
	}
	return isUserFound
}

package voice

import (
	"errors"
	"strings"

	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
	tlguard "mumble.info/grumble/pkg/teamlancer/auth/guard"
)

const ChannelNamePrefix = "teamlancer-board-"

var (
	ErrJoinDenied   = errors.New("voice room join denied")
	ErrMissingBoard = errors.New("voice room requires board id")
	ErrMissingUser  = errors.New("voice room requires user id")
)

func CanJoinVoice(identity *tlauth.UserIdentity) bool {
	return tlguard.CanJoinVoice(identity)
}

func CanJoinRoom(identity *tlauth.UserIdentity, room *VoiceRoom) bool {
	return CanJoinVoice(identity) && sameBoard(identity, room)
}

func CanMoveChannel(identity *tlauth.UserIdentity, targetChannel *VoiceRoom) bool {
	return sameBoard(identity, targetChannel)
}

func CanReceiveFromRoom(identity *tlauth.UserIdentity, room *VoiceRoom) bool {
	return sameBoard(identity, room)
}

func sameBoard(identity *tlauth.UserIdentity, room *VoiceRoom) bool {
	if identity == nil || room == nil {
		return false
	}
	boardID := strings.TrimSpace(identity.BoardID)
	return boardID != "" && boardID == strings.TrimSpace(room.BoardID)
}

func ChannelName(boardID string) string {
	return ChannelNamePrefix + strings.TrimSpace(boardID)
}

func RoomID(teamID, boardID string) string {
	teamID = strings.TrimSpace(teamID)
	boardID = strings.TrimSpace(boardID)
	if teamID == "" {
		return ChannelName(boardID)
	}
	return teamID + ":" + boardID
}

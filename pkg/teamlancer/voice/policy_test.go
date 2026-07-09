package voice

import (
	"testing"

	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
)

func TestCanMoveChannelSameBoard(t *testing.T) {
	identity := &tlauth.UserIdentity{BoardID: "board-1"}
	room := &VoiceRoom{BoardID: "board-1"}
	if !CanMoveChannel(identity, room) {
		t.Fatal("expected same-board move to be allowed")
	}
}

func TestCanMoveChannelDifferentBoardDenied(t *testing.T) {
	identity := &tlauth.UserIdentity{BoardID: "board-1"}
	room := &VoiceRoom{BoardID: "board-2"}
	if CanMoveChannel(identity, room) {
		t.Fatal("expected cross-board move to be denied")
	}
}

func TestCanReceiveFromRoomDifferentBoardDenied(t *testing.T) {
	identity := &tlauth.UserIdentity{BoardID: "board-1"}
	room := &VoiceRoom{BoardID: "board-2"}
	if CanReceiveFromRoom(identity, room) {
		t.Fatal("expected cross-board receive to be denied")
	}
}

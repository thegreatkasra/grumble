package voice

import (
	"testing"
	"time"

	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
)

func TestSameBoardJoinsSameRoom(t *testing.T) {
	manager := NewVoiceRoomManager()
	manager.now = func() time.Time { return time.Unix(100, 0).UTC() }

	alice := &tlauth.UserIdentity{
		UserID:      "alice",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}
	bob := &tlauth.UserIdentity{
		UserID:      "bob",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}

	first, created, err := manager.Join(alice, 11)
	if err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	if !created {
		t.Fatal("expected first join to create room")
	}

	second, created, err := manager.Join(bob, 11)
	if err != nil {
		t.Fatalf("second join failed: %v", err)
	}
	if created {
		t.Fatal("expected second join to reuse room")
	}
	if first.RoomID != second.RoomID {
		t.Fatalf("expected same room id, got %q and %q", first.RoomID, second.RoomID)
	}
	if second.ChannelID != 11 {
		t.Fatalf("expected channel id 11, got %d", second.ChannelID)
	}
	if second.ParticipantCount() != 2 {
		t.Fatalf("expected two participants, got %d", second.ParticipantCount())
	}
}

func TestDifferentBoardsCreateDifferentRooms(t *testing.T) {
	manager := NewVoiceRoomManager()

	first, _, err := manager.Join(&tlauth.UserIdentity{
		UserID:      "alice",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}, 11)
	if err != nil {
		t.Fatalf("first join failed: %v", err)
	}
	second, _, err := manager.Join(&tlauth.UserIdentity{
		UserID:      "bob",
		TeamID:      "team-1",
		BoardID:     "board-2",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}, 12)
	if err != nil {
		t.Fatalf("second join failed: %v", err)
	}

	if first.RoomID == second.RoomID {
		t.Fatalf("expected different room ids, got %q", first.RoomID)
	}
	if first.ChannelID == second.ChannelID {
		t.Fatalf("expected different channels, got %d", first.ChannelID)
	}
}

func TestDeniedUserCannotJoin(t *testing.T) {
	manager := NewVoiceRoomManager()

	_, _, err := manager.Join(&tlauth.UserIdentity{
		UserID:      "alice",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: false},
	}, 11)
	if err != ErrJoinDenied {
		t.Fatalf("expected ErrJoinDenied, got %v", err)
	}
}

func TestEmptyRoomCleanup(t *testing.T) {
	manager := NewVoiceRoomManager()

	room, _, err := manager.Join(&tlauth.UserIdentity{
		UserID:      "alice",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}, 11)
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}

	left, empty, destroyed := manager.Leave(room.BoardID, "alice")
	if !empty || !destroyed {
		t.Fatalf("expected empty destroyed room, got empty=%v destroyed=%v", empty, destroyed)
	}
	if left == nil || left.RoomID != room.RoomID {
		t.Fatalf("expected snapshot for room %q", room.RoomID)
	}
	if _, ok := manager.Find(room.BoardID); ok {
		t.Fatal("expected room to be cleaned up")
	}
}

func TestFindByChannelID(t *testing.T) {
	manager := NewVoiceRoomManager()

	room, _, err := manager.Join(&tlauth.UserIdentity{
		UserID:      "alice",
		TeamID:      "team-1",
		BoardID:     "board-1",
		Permissions: tlauth.Permissions{JoinVoice: true},
	}, 42)
	if err != nil {
		t.Fatalf("join failed: %v", err)
	}

	found, ok := manager.FindByChannelID(42)
	if !ok {
		t.Fatal("expected room lookup by channel id to succeed")
	}
	if found.BoardID != room.BoardID {
		t.Fatalf("expected board id %q, got %q", room.BoardID, found.BoardID)
	}
}

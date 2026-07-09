package voice

import (
	"strings"
	"sync"
	"time"

	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
)

type VoiceRoomManager struct {
	mu           sync.Mutex
	now          func() time.Time
	roomsByBoard map[string]*VoiceRoom
}

func NewVoiceRoomManager() *VoiceRoomManager {
	return &VoiceRoomManager{
		now:          time.Now,
		roomsByBoard: make(map[string]*VoiceRoom),
	}
}

func (m *VoiceRoomManager) Find(boardID string) (*VoiceRoom, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	room, ok := m.roomsByBoard[strings.TrimSpace(boardID)]
	if !ok {
		return nil, false
	}
	return cloneRoom(room), true
}

func (m *VoiceRoomManager) FindByChannelID(channelID int) (*VoiceRoom, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, room := range m.roomsByBoard {
		if room.ChannelID == channelID {
			return cloneRoom(room), true
		}
	}
	return nil, false
}

func (m *VoiceRoomManager) Join(identity *tlauth.UserIdentity, channelID int) (*VoiceRoom, bool, error) {
	if !CanJoinVoice(identity) {
		return nil, false, ErrJoinDenied
	}
	boardID := strings.TrimSpace(identity.BoardID)
	if boardID == "" {
		return nil, false, ErrMissingBoard
	}
	userID := strings.TrimSpace(identity.UserID)
	if userID == "" {
		return nil, false, ErrMissingUser
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	room, ok := m.roomsByBoard[boardID]
	created := false
	if !ok {
		room = &VoiceRoom{
			RoomID:       RoomID(identity.TeamID, boardID),
			TeamID:       strings.TrimSpace(identity.TeamID),
			BoardID:      boardID,
			ChannelID:    channelID,
			CreatedAt:    m.now().UTC(),
			Participants: map[string]struct{}{},
		}
		m.roomsByBoard[boardID] = room
		created = true
	}
	room.Participants[userID] = struct{}{}
	return cloneRoom(room), created, nil
}

func (m *VoiceRoomManager) Leave(boardID, userID string) (*VoiceRoom, bool, bool) {
	boardID = strings.TrimSpace(boardID)
	userID = strings.TrimSpace(userID)
	if boardID == "" || userID == "" {
		return nil, false, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	room, ok := m.roomsByBoard[boardID]
	if !ok {
		return nil, false, false
	}
	delete(room.Participants, userID)
	snapshot := cloneRoom(room)
	empty := len(room.Participants) == 0
	if empty {
		delete(m.roomsByBoard, boardID)
	}
	return snapshot, empty, empty
}

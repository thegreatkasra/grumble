package voice

import "time"

type VoiceRoom struct {
	RoomID       string
	TeamID       string
	BoardID      string
	ChannelID    int
	CreatedAt    time.Time
	Participants map[string]struct{}
}

func (room *VoiceRoom) ParticipantCount() int {
	if room == nil {
		return 0
	}
	return len(room.Participants)
}

func cloneRoom(room *VoiceRoom) *VoiceRoom {
	if room == nil {
		return nil
	}
	cloned := &VoiceRoom{
		RoomID:       room.RoomID,
		TeamID:       room.TeamID,
		BoardID:      room.BoardID,
		ChannelID:    room.ChannelID,
		CreatedAt:    room.CreatedAt,
		Participants: make(map[string]struct{}, len(room.Participants)),
	}
	for userID := range room.Participants {
		cloned.Participants[userID] = struct{}{}
	}
	return cloned
}

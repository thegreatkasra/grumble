package auth

import "strings"

type Permissions struct {
	JoinVoice     bool
	PublishAudio  bool
	ReceiveAudio  bool
	ModerateVoice bool
}

func DefaultPermissions() Permissions {
	return Permissions{
		JoinVoice:    true,
		PublishAudio: true,
		ReceiveAudio: true,
	}
}

func ParsePermissionName(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "joinvoice", "join_voice":
		return "join_voice", true
	case "publishaudio", "publish_audio":
		return "publish_audio", true
	case "receiveaudio", "receive_audio":
		return "receive_audio", true
	case "moderatevoice", "moderate_voice":
		return "moderate_voice", true
	default:
		return "", false
	}
}

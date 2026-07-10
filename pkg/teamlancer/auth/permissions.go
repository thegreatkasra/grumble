package auth

import "strings"

const (
	PermissionVoiceJoin     = "voice.join"
	PermissionVoicePublish  = "voice.publish"
	PermissionVoiceReceive  = "voice.receive"
	PermissionVoiceModerate = "voice.moderate"
)

type Permissions struct {
	JoinVoice     bool
	PublishAudio  bool
	ReceiveAudio  bool
	ModerateVoice bool
	// Presented is the whitespace-trimmed permission strings from the JWT claim,
	// in claim order. Unknown names are preserved for diagnostics.
	Presented []string
}

func DefaultPermissions() Permissions {
	return Permissions{
		JoinVoice:    true,
		PublishAudio: true,
		ReceiveAudio: true,
	}
}

func ParsePermissionName(name string) (string, bool) {
	normalized := strings.TrimSpace(name)
	switch normalized {
	case PermissionVoiceJoin:
		return PermissionVoiceJoin, true
	case PermissionVoicePublish:
		return PermissionVoicePublish, true
	case PermissionVoiceReceive:
		return PermissionVoiceReceive, true
	case PermissionVoiceModerate:
		return PermissionVoiceModerate, true
	default:
		return "", false
	}
}

func (p Permissions) PresentedNames() []string {
	if len(p.Presented) > 0 {
		out := make([]string, len(p.Presented))
		copy(out, p.Presented)
		return out
	}
	out := make([]string, 0, 4)
	if p.JoinVoice {
		out = append(out, PermissionVoiceJoin)
	}
	if p.PublishAudio {
		out = append(out, PermissionVoicePublish)
	}
	if p.ReceiveAudio {
		out = append(out, PermissionVoiceReceive)
	}
	if p.ModerateVoice {
		out = append(out, PermissionVoiceModerate)
	}
	return out
}

package guard

import tlauth "mumble.info/grumble/pkg/teamlancer/auth"

func CanJoinVoice(identity *tlauth.UserIdentity) bool {
	if identity == nil {
		return false
	}
	return identity.Permissions.JoinVoice
}

func CanPublishAudio(identity *tlauth.UserIdentity) bool {
	if identity == nil {
		return false
	}
	return identity.Permissions.PublishAudio
}

func CanReceiveAudio(identity *tlauth.UserIdentity) bool {
	if identity == nil {
		return false
	}
	return identity.Permissions.ReceiveAudio
}

func CanModerateVoice(identity *tlauth.UserIdentity) bool {
	if identity == nil {
		return false
	}
	return identity.Permissions.ModerateVoice
}

type ModerationHooks interface {
	CanMuteUser(actor *tlauth.UserIdentity, target *tlauth.UserIdentity) bool
	CanMoveUser(actor *tlauth.UserIdentity, target *tlauth.UserIdentity) bool
	CanDisconnectUser(actor *tlauth.UserIdentity, target *tlauth.UserIdentity) bool
}

type PermissionModerationHooks struct{}

func (PermissionModerationHooks) CanMuteUser(actor *tlauth.UserIdentity, _ *tlauth.UserIdentity) bool {
	return CanModerateVoice(actor)
}

func (PermissionModerationHooks) CanMoveUser(actor *tlauth.UserIdentity, _ *tlauth.UserIdentity) bool {
	return CanModerateVoice(actor)
}

func (PermissionModerationHooks) CanDisconnectUser(actor *tlauth.UserIdentity, _ *tlauth.UserIdentity) bool {
	return CanModerateVoice(actor)
}

package guard

import (
	"testing"

	tlauth "mumble.info/grumble/pkg/teamlancer/auth"
)

func TestCanJoinVoice(t *testing.T) {
	identity := &tlauth.UserIdentity{Permissions: tlauth.Permissions{JoinVoice: true}}
	if !CanJoinVoice(identity) {
		t.Fatal("expected join permission to allow voice")
	}
	identity.Permissions.JoinVoice = false
	if CanJoinVoice(identity) {
		t.Fatal("expected join permission to deny voice")
	}
}

func TestCanPublishAudio(t *testing.T) {
	identity := &tlauth.UserIdentity{Permissions: tlauth.Permissions{PublishAudio: true}}
	if !CanPublishAudio(identity) {
		t.Fatal("expected publish permission to allow audio")
	}
	identity.Permissions.PublishAudio = false
	if CanPublishAudio(identity) {
		t.Fatal("expected publish permission to block audio")
	}
}

func TestCanReceiveAudio(t *testing.T) {
	identity := &tlauth.UserIdentity{Permissions: tlauth.Permissions{ReceiveAudio: true}}
	if !CanReceiveAudio(identity) {
		t.Fatal("expected receive permission to allow audio")
	}
	identity.Permissions.ReceiveAudio = false
	if CanReceiveAudio(identity) {
		t.Fatal("expected receive permission to block audio")
	}
}

func TestModerationHookExists(t *testing.T) {
	var hooks ModerationHooks = PermissionModerationHooks{}

	actor := &tlauth.UserIdentity{Permissions: tlauth.Permissions{ModerateVoice: true}}
	target := &tlauth.UserIdentity{}
	if !hooks.CanMuteUser(actor, target) {
		t.Fatal("expected mute hook to exist and allow moderator")
	}
	if !hooks.CanMoveUser(actor, target) {
		t.Fatal("expected move hook to exist and allow moderator")
	}
	if !hooks.CanDisconnectUser(actor, target) {
		t.Fatal("expected disconnect hook to exist and allow moderator")
	}
}

package auth

import "testing"

func TestParsePermissionNameCanonicalOnly(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"voice.join", PermissionVoiceJoin, true},
		{" voice.publish ", PermissionVoicePublish, true},
		{"voice.receive", PermissionVoiceReceive, true},
		{"voice.moderate", PermissionVoiceModerate, true},
		{"join_voice", "", false},
		{"join", "", false},
		{"voice:join", "", false},
		{"VOICE.JOIN", "", false},
	}
	for _, tc := range cases {
		got, ok := ParsePermissionName(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("ParsePermissionName(%q)=(%q,%v) want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPresentedNamesFallsBackToFlags(t *testing.T) {
	perms := Permissions{JoinVoice: true, PublishAudio: true}
	got := perms.PresentedNames()
	if len(got) != 2 || got[0] != PermissionVoiceJoin || got[1] != PermissionVoicePublish {
		t.Fatalf("unexpected presented names: %+v", got)
	}
}

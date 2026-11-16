package state

import "testing"

// Every kind of the screen, tmux and sbx is evidence of one group, the
// forms of one thing sharing it; the hook's kinds and hq's own are none.
func TestEveryKindOfEvidenceHasItsGroup(t *testing.T) {
	for kind, want := range map[string]string{
		KindTurnEnded: EvidenceTurnEnd, KindRewound: EvidenceTurnEnd, KindTurnNotTaken: EvidenceTurnEnd,
		KindAtRest: EvidenceAtRest, KindToolCut: EvidenceAtRest,
		KindOwnDialog: EvidenceOwnDialog, KindOwnDialogGone: EvidenceOwnDialog,
		KindAtWork: EvidenceAtWork, KindTookQueued: EvidenceTookQueued, KindWokeUp: EvidenceWokeUp,
		KindPaneDied: EvidencePaneDied, KindNotRunning: EvidenceNotRunning,
		KindLaunched: "", KindRelaunched: "", KindTakenDown: "", KindAnswer: "", KindWithdrawn: "", KindDelivery: "",
		KindPrompt: "", KindTurnEnd: "", KindPermission: "", KindSessionStart: "", KindSessionEnd: "", KindUnreadable: "",
	} {
		if got := EvidenceOf(kind); got != want {
			t.Errorf("%s: group %q, want %q", kind, got, want)
		}
	}
}

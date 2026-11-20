package state

// The groups of evidence: what the screen, tmux and sbx show of an agent
// that no hook reports. An event of a group stands for one thing a look saw
// for the hook's report it was based on, and a later event of the same
// group replaces it: the reducer keeps one mark per group (Fold.see), and
// the journal holds one standing event per group (journal.Evidence). The
// kinds of a group are the forms that one thing takes: a turn the user
// ended is a line Claude printed, a rewind or a turn Claude owed that does
// not come; a screen at rest is the bare prompt box or a tool cut short.
// The first prompt taken as a command of Claude's own (KindTookCommand) is
// no turn end: it ends no turn, and completes none.
const (
	EvidenceTurnEnd     = "turn end"
	EvidenceAtRest      = "at rest"
	EvidenceOwnDialog   = "own dialog"
	EvidenceAtWork      = "at work"
	EvidenceTookQueued  = "took queued"
	EvidenceWokeUp      = "woke up"
	EvidenceTookCommand = "took command"
	EvidencePaneDied    = "pane died"
	EvidenceNotRunning  = "not running"
)

// evidenceGroups is the group of every kind of evidence.
var evidenceGroups = map[string]string{
	KindTurnEnded: EvidenceTurnEnd, KindRewound: EvidenceTurnEnd, KindTurnNotTaken: EvidenceTurnEnd,
	KindAtRest: EvidenceAtRest, KindToolCut: EvidenceAtRest,
	KindOwnDialog: EvidenceOwnDialog, KindOwnDialogGone: EvidenceOwnDialog,
	KindAtWork:      EvidenceAtWork,
	KindTookQueued:  EvidenceTookQueued,
	KindWokeUp:      EvidenceWokeUp,
	KindTookCommand: EvidenceTookCommand,
	KindPaneDied:    EvidencePaneDied,
	KindNotRunning:  EvidenceNotRunning,
}

// EvidenceOf is the group of evidence of an event of kind kind, "" for a
// kind that is no evidence: the hook's kinds and hq's own. The kinds are
// unique across their sources, so the kind alone tells it.
func EvidenceOf(kind string) string { return evidenceGroups[kind] }

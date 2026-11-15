package state

import (
	"testing"
	"time"
)

// An agent's screen can overrule its hooks' report from OverruleDelay after
// that report on, not a moment before.
func TestTheScreenOverrulesAReportFromOverruleDelayOn(t *testing.T) {
	f := Fold{}.With(userPrompt(1, "t1"))
	if f.ScreenMayTell(atSecond(1).Add(OverruleDelay - time.Nanosecond)) {
		t.Error("may tell before OverruleDelay")
	}
	if !f.ScreenMayTell(atSecond(1).Add(OverruleDelay)) {
		t.Error("may not tell at OverruleDelay")
	}
	if (Fold{}).ScreenMayTell(atSecond(9)) {
		t.Error("may tell before any report")
	}
}

// hq takes an agent down only by its own event: the pane's death or sbx's
// answer is no take-down, and a launch starts over.
func TestOnlyARestartTakesAnAgentDown(t *testing.T) {
	f := Fold{}.With(userPrompt(1, "t1"), seenEvent(2, SourceTmux, KindPaneDied, 1, Facts{}))
	if f.TakenDown() {
		t.Fatal("the pane's death took it down")
	}
	f = f.With(seenEvent(3, SourceHQ, KindTakenDown, 1, Facts{}))
	if !f.TakenDown() {
		t.Fatal("a restart did not take it down")
	}
	if f.With(seenEvent(4, SourceHQ, KindRelaunched, 1, Facts{})).TakenDown() {
		t.Fatal("still taken down after the relaunch")
	}
}

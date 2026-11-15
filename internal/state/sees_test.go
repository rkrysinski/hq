package state

import (
	"testing"
	"time"
)

// An agent's screen can overrule its hooks' report from SettleDelay after
// that report on, not a moment before.
func TestTheScreenOverrulesAReportFromSettleDelayOn(t *testing.T) {
	f := Fold{}.With(userPrompt(1, "t1"))
	if f.Unsettled(atSecond(1).Add(SettleDelay - time.Nanosecond)) {
		t.Error("unsettled before SettleDelay")
	}
	if !f.Unsettled(atSecond(1).Add(SettleDelay)) {
		t.Error("settled at SettleDelay")
	}
	if (Fold{}).Unsettled(atSecond(9)) {
		t.Error("unsettled before any report")
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

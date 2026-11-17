package biz

import (
	"strings"
	"testing"
)

// C13: the sharing verdict — an hour above the threshold on 3 of 7 days, or 4 hours on any day.
func TestJudgeSharing(t *testing.T) {
	week := func(over ...int) []DialDay {
		out := make([]DialDay, 7)
		for i, m := range over {
			out[i].MinutesOver = m
		}
		return out
	}
	if v := JudgeSharing(week()); v.Suspect || v.Text != "В норме" {
		t.Errorf("quiet week: %+v", v)
	}
	if v := JudgeSharing(week(60, 0, 61, 0, 59)); v.Suspect || v.DaysOver != 2 || !strings.Contains(v.Text, "до 61 мин") {
		t.Errorf("two hour-days: %+v", v)
	}
	if v := JudgeSharing(week(60, 70, 80)); !v.Suspect || v.DaysOver != 3 || !strings.Contains(v.Text, "3 из 7") {
		t.Errorf("three hour-days: %+v", v)
	}
	if v := JudgeSharing(week(0, 240)); !v.Suspect || v.MaxMinute != 240 {
		t.Errorf("one heavy day: %+v", v)
	}
}

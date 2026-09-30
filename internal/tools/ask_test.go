package tools

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A state that travels through the client is refused once it expires,
// and every state is redeemed once.
func TestAStateExpiresAndIsSpentOnce(t *testing.T) {
	a := newAsking(slog.New(slog.DiscardHandler))
	now := time.Now()
	fresh := a.sign(askState{Tool: "delete_tab", Args: "a", Question: "q", Nonce: "n1", Expires: now.Add(time.Minute).Unix()})
	late := a.sign(askState{Tool: "delete_tab", Args: "a", Question: "q", Nonce: "n2", Expires: now.Add(-time.Minute).Unix()})
	if _, err := a.redeem(late, "delete_tab", "a", now); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("late: %v", err)
	}
	if _, err := a.redeem(fresh, "delete_tab", "a", now); err != nil {
		t.Errorf("fresh: %v", err)
	}
	if _, err := a.redeem(fresh, "delete_tab", "a", now); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("spent: %v", err)
	}
}

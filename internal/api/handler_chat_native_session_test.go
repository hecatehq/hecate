package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

func TestChatActivitiesContainType(t *testing.T) {
	t.Parallel()
	activities := []chat.Activity{{Type: "running"}, {Type: "session_recovery"}}
	if !chatActivitiesContainType(activities, "session_recovery") {
		t.Fatal("chatActivitiesContainType() = false, want true")
	}
	if chatActivitiesContainType(activities, "recovered") {
		t.Fatal("chatActivitiesContainType() = true for absent type")
	}
}

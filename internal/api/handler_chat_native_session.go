package api

import (
	"strings"

	"github.com/hecatehq/hecate/internal/chat"
)

func chatActivitiesContainType(activities []chat.Activity, activityType string) bool {
	activityType = strings.TrimSpace(activityType)
	for _, activity := range activities {
		if strings.TrimSpace(activity.Type) == activityType {
			return true
		}
	}
	return false
}

package stories

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

// sortPeople: people with something unseen first, then the newest story.
func sortPeople(people []Person, latest map[uuid.UUID]time.Time) {
	slices.SortStableFunc(people, func(a, b Person) int {
		if a.AllSeen != b.AllSeen {
			if !a.AllSeen {
				return -1
			}
			return 1
		}
		return latest[b.UserID].Compare(latest[a.UserID])
	})
}

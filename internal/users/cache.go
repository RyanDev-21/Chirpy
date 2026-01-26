package users

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	cachepkg "RyanDev-21.com/Chirpy/pkg/cache"
)

// FriendRequest is a small example payload stored in cache.
type FriendRequest struct {
	ID        string    `json:"id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func friendRequestKey(reqID string) string { return fmt.Sprintf("friendrequest:%s", reqID) }
func userIncomingKey(userID string) string { return fmt.Sprintf("users:%s:friend:incoming", userID) }
func userOutgoingKey(userID string) string { return fmt.Sprintf("users:%s:friend:outgoing", userID) }

// AddFriendRequest stores the request and adds indices for incoming/outgoing.
func AddFriendRequest(ctx context.Context, c cachepkg.Cache, req FriendRequest, ttl time.Duration) error {
	// store the request object
	if err := cachepkg.SetJSON(c, ctx, friendRequestKey(req.ID), req, ttl); err != nil {
		return err
	}

	// add to sender outgoing list
	if err := addIDToIndex(ctx, c, userOutgoingKey(req.FromID), req.ID, ttl); err != nil {
		return err
	}
	// add to recipient incoming list
	if err := addIDToIndex(ctx, c, userIncomingKey(req.ToID), req.ID, ttl); err != nil {
		return err
	}
	return nil
}

// addIDToIndex keeps a simple JSON array of ids for the index key.
func addIDToIndex(ctx context.Context, c cachepkg.Cache, indexKey, id string, ttl time.Duration) error {
	var list []string
	b, err := c.Get(ctx, indexKey)
	if err == nil && b != nil {
		_ = json.Unmarshal(b, &list)
	}
	// append if not present
	for _, v := range list {
		if v == id {
			// already present
			return nil
		}
	}
	list = append(list, id)
	nb, _ := json.Marshal(list)
	return c.Set(ctx, indexKey, nb, ttl)
}

// GetIncomingRequests returns deserialized FriendRequest objects for a user.
func GetIncomingRequests(ctx context.Context, c cachepkg.Cache, userID string) ([]FriendRequest, error) {
	idxKey := userIncomingKey(userID)
	b, err := c.Get(ctx, idxKey)
	if err != nil {
		// treat miss as empty list
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		return nil, err
	}
	var out []FriendRequest
	for _, id := range ids {
		fb, err := c.Get(ctx, friendRequestKey(id))
		if err != nil {
			continue
		}
		var fr FriendRequest
		if err := json.Unmarshal(fb, &fr); err != nil {
			continue
		}
		out = append(out, fr)
	}
	return out, nil
}

// AcceptFriendRequest marks a friend request accepted and removes indices.
func AcceptFriendRequest(ctx context.Context, c cachepkg.Cache, reqID string) error {
	key := friendRequestKey(reqID)
	b, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	var fr FriendRequest
	if err := json.Unmarshal(b, &fr); err != nil {
		return err
	}
	fr.Status = "accepted"
	if err := cachepkg.SetJSON(c, ctx, key, fr, 0); err != nil {
		return err
	}
	// remove from indices (simple remove by re-writing list)
	_ = removeIDFromIndex(ctx, c, userIncomingKey(fr.ToID), reqID)
	_ = removeIDFromIndex(ctx, c, userOutgoingKey(fr.FromID), reqID)
	return nil
}

func removeIDFromIndex(ctx context.Context, c cachepkg.Cache, indexKey, id string) error {
	b, err := c.Get(ctx, indexKey)
	if err != nil || b == nil {
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	var newList []string
	for _, v := range list {
		if v != id {
			newList = append(newList, v)
		}
	}
	nb, _ := json.Marshal(newList)
	return c.Set(ctx, indexKey, nb, 0)
}

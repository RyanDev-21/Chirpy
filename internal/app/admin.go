package app

import (
	"net/http"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/platform/httpx"
)

type adminHandler struct {
	queries *database.Queries
}

// ResetUsers deletes every user. Only mounted when PLATFORM=dev.
func (h *adminHandler) ResetUsers(w http.ResponseWriter, r *http.Request) {
	if err := h.queries.DeleteUser(r.Context()); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"msg": "successfully deleted"})
}

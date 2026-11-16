package httpapi

import (
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

func (h *Handlers) decidePlan(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	planID := r.PathValue("plan_id")
	if !uuid.Valid(planID) {
		h.writeStoreError(w, r, runtime.ErrPlanNotFound)
		return
	}
	var decision runtime.PlanDecision
	if !decodeInput(w, r, &decision) {
		return
	}
	decision.PlanID = strings.ToLower(planID)
	for i, id := range decision.AcceptedItemIDs {
		if !uuid.Valid(id) {
			invalidInput(w, r)
			return
		}
		decision.AcceptedItemIDs[i] = strings.ToLower(id)
	}
	h.startRun(w, r, user, id, "", true, nil, &decision)
}

func (h *Handlers) reviewPlan(w http.ResponseWriter, r *http.Request, user identity.User) {
	id, ok := conversationID(w, r)
	if !ok {
		return
	}
	if !uuid.Valid(r.PathValue("plan_id")) {
		h.writeStoreError(w, r, runtime.ErrPlanNotFound)
		return
	}
	h.startRun(w, r, user, id, "", true, nil)
}

package admin

import (
	"net/http"

	"github.com/heyearth/backend/internal/biz"
)

// Hysteria2StatusHandler returns the Express subsystem snapshot for rendering
// on the Hub server detail page. See biz.GetHysteria2Status for the shape.
func Hysteria2StatusHandler(w http.ResponseWriter, r *http.Request) {
	writeAdminJSON(w, http.StatusOK, biz.GetHysteria2Status())
}

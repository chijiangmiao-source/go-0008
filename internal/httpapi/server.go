package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
)

type Server struct {
	service *app.Service
	mux     *http.ServeMux
}

func New(service *app.Service) *Server {
	s := &Server{service: service, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /v1/operations", s.createOperation)
	s.mux.HandleFunc("/v1/operations/", s.operationRoutes)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createOperation(w http.ResponseWriter, r *http.Request) {
	var req app.CreateOperationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.service.CreateOperation(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) operationRoutes(w http.ResponseWriter, r *http.Request) {
	id, tail, ok := splitOperationPath(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		s.getOperation(w, r, id)
	case r.Method == http.MethodPost && tail == "seal":
		s.seal(w, r, id)
	case r.Method == http.MethodPost && tail == "readings":
		s.readings(w, r, id)
	case r.Method == http.MethodGet && tail == "evidence":
		s.evidence(w, r, id)
	case r.Method == http.MethodPost && strings.HasPrefix(tail, "deviations/") && strings.HasSuffix(tail, "/resolve"):
		parts := strings.Split(tail, "/")
		if len(parts) != 3 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		s.resolveDeviation(w, r, id, parts[1])
	case r.Method == http.MethodPost && tail == "ventilation/commands":
		s.ventilation(w, r, id)
	case r.Method == http.MethodPost && tail == "emergency-stop":
		s.emergencyStop(w, r, id)
	case r.Method == http.MethodPost && tail == "reset":
		s.reset(w, r, id)
	case r.Method == http.MethodPost && tail == "entry-permit":
		s.entry(w, r, id)
	case r.Method == http.MethodPost && tail == "archive":
		s.archive(w, r, id)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	}
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request, id string) {
	resp, err := s.service.Get(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) seal(w http.ResponseWriter, r *http.Request, id string) {
	var req app.SealRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.Seal(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) readings(w http.ResponseWriter, r *http.Request, id string) {
	var req app.ReadingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.service.SubmitReadings(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) evidence(w http.ResponseWriter, r *http.Request, id string) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	resp, err := s.service.Evidence(r.Context(), id, offset, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) resolveDeviation(w http.ResponseWriter, r *http.Request, id, deviationID string) {
	var req domain.ResolveDeviationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.ResolveDeviation(r.Context(), id, deviationID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) ventilation(w http.ResponseWriter, r *http.Request, id string) {
	var req app.VentilationCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.VentilationCommand(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) emergencyStop(w http.ResponseWriter, r *http.Request, id string) {
	var req app.EmergencyStopRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.EmergencyStop(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request, id string) {
	var req app.ResetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.Reset(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) entry(w http.ResponseWriter, r *http.Request, id string) {
	var req app.EntryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	snap, err := s.service.EntryPermit(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) archive(w http.ResponseWriter, r *http.Request, id string) {
	resp, err := s.service.Archive(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func splitOperationPath(path string) (string, string, bool) {
	trimmed := strings.TrimPrefix(path, "/v1/operations/")
	if trimmed == path || trimmed == "" {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if parts[0] == "" {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", true
	}
	return parts[0], parts[1], true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json", "message": err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	var validation domain.ValidationError
	var conflict domain.ConflictError
	var boundary domain.BoundaryError
	var notFound domain.NotFoundError
	switch {
	case errors.As(err, &validation):
		writeJSON(w, http.StatusBadRequest, validation)
	case errors.As(err, &boundary):
		writeJSON(w, http.StatusUnprocessableEntity, boundary)
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, conflict)
	case errors.As(err, &notFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": notFound.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal", "message": err.Error()})
	}
}

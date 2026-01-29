package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchids-api/internal/clerk"
	"orchids-api/internal/store"
)

type API struct {
	store *store.Store
}

type ExportData struct {
	Version   int              `json:"version"`
	ExportAt  time.Time        `json:"export_at"`
	Accounts  []store.Account  `json:"accounts"`
}

type ImportResult struct {
	Total    int `json:"total"`
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

func New(s *store.Store) *API {
	return &API{store: s}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/accounts", a.HandleAccounts)
	mux.HandleFunc("/api/accounts/", a.HandleAccountByID)
}

func (a *API) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		accounts, err := a.store.ListAccounts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(accounts)

	case http.MethodPost:
		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if acc.ClientCookie != "" && acc.SessionID == "" {
			info, err := clerk.FetchAccountInfo(acc.ClientCookie)
			if err != nil {
				log.Printf("Failed to fetch account info: %v", err)
				http.Error(w, "Failed to fetch account info: "+err.Error(), http.StatusBadRequest)
				return
			}
			acc.SessionID = info.SessionID
			acc.ClientUat = info.ClientUat
			acc.ProjectID = info.ProjectID
			acc.UserID = info.UserID
			acc.Email = info.Email
		}

		if err := a.store.CreateAccount(&acc); err != nil {
			log.Printf("Failed to create account: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(acc)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) HandleAccountByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		acc, err := a.store.GetAccount(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(acc)

	case http.MethodPut:
		existing, err := a.store.GetAccount(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var acc store.Account
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		acc.ID = id

		if acc.SessionID == "" {
			acc.SessionID = existing.SessionID
		}
		if acc.ClientUat == "" {
			acc.ClientUat = existing.ClientUat
		}
		if acc.ProjectID == "" {
			acc.ProjectID = existing.ProjectID
		}
		if acc.UserID == "" {
			acc.UserID = existing.UserID
		}
		if acc.Email == "" {
			acc.Email = existing.Email
		}

		if err := a.store.UpdateAccount(&acc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(acc)

	case http.MethodDelete:
		if err := a.store.DeleteAccount(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	accounts, err := a.store.ListAccounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exportData := ExportData{
		Version:  1,
		ExportAt: time.Now(),
		Accounts: make([]store.Account, len(accounts)),
	}
	for i, acc := range accounts {
		exportData.Accounts[i] = *acc
		exportData.Accounts[i].ID = 0
		exportData.Accounts[i].RequestCount = 0
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=accounts_export.json")
	json.NewEncoder(w).Encode(exportData)
}

func (a *API) HandleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var exportData ExportData
	if err := json.NewDecoder(r.Body).Decode(&exportData); err != nil {
		http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	result := ImportResult{Total: len(exportData.Accounts)}

	for _, acc := range exportData.Accounts {
		acc.ID = 0
		acc.RequestCount = 0
		if err := a.store.CreateAccount(&acc); err != nil {
			log.Printf("Failed to import account %s: %v", acc.Name, err)
			result.Skipped++
		} else {
			result.Imported++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

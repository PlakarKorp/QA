// mock-parent stands in for core-api during QA runs.
//
// plakman only ever asks its parent for four things: who is logging in, which
// organizations they have, a badge naming one of them, and a license. All of it
// is JWT signed by the parent and verified through the parent's own PKI
// endpoint, so the mock is self-consistent: it serves the key it signs with and
// nothing needs to be pinned on the plakman side.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/PlakarKorp/plakman/api/v1/contract"
	"github.com/PlakarKorp/plakman/credentials"
	"github.com/PlakarKorp/plakman/pki"
	"github.com/google/uuid"
)

const keyId = "mock-parent"

type parent struct {
	mu   sync.Mutex
	jwt  credentials.JWT
	keys *pki.PkiMemory

	orgs   map[uuid.UUID]contract.OrganizationResponse
	polls  map[string]string // poll id -> completion code
	user   uuid.UUID
	access uuid.UUID
}

func newParent() (*parent, error) {
	kp, err := pki.GenerateKeyPair(pki.ED25519)
	if err != nil {
		return nil, err
	}
	keys := pki.NewPkiMemory()
	if err := keys.AddKeyPair(keyId, *kp); err != nil {
		return nil, err
	}
	return &parent{
		jwt:    credentials.JWT{Pki: keys},
		keys:   keys,
		orgs:   make(map[uuid.UUID]contract.OrganizationResponse),
		polls:  make(map[string]string),
		user:   uuid.New(),
		access: uuid.New(),
	}, nil
}

func reply(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func decode(r *http.Request, into any) bool {
	return json.NewDecoder(r.Body).Decode(into) == nil
}

// badge mints the token plakman treats as proof of "this caller acts in this
// organization". Access and User have to parse as <uuid>:<revision> even when
// the mock has nothing meaningful to put there.
func (p *parent) badge(org uuid.UUID) (string, error) {
	now := time.Now().Truncate(time.Second)
	b := credentials.Badge{
		IssuedAt:     now,
		ExpiresAt:    now.Add(time.Hour),
		Version:      1,
		User:         credentials.VersionizedUUID{Id: p.user},
		Organization: credentials.VersionizedUUID{Id: org},
		Access:       credentials.VersionizedUUID{Id: p.access},
	}
	signed, _, err := p.jwt.SignedString(&b)
	return signed, err
}

func (p *parent) anyOrg() uuid.UUID {
	for id := range p.orgs {
		return id
	}
	return uuid.Nil
}

func main() {
	addr := flag.String("listen", ":8081", "listen address")
	flag.Parse()

	p, err := newParent()
	if err != nil {
		log.Fatalf("mock-parent: %v", err)
	}

	mux := http.NewServeMux()

	// The PKI endpoint every verification path funnels through.
	mux.HandleFunc("GET /api/v1/pki/keys/{kid}", func(w http.ResponseWriter, r *http.Request) {
		key, err := p.keys.GetVerifyKey(r.PathValue("kid"))
		if err != nil {
			reply(w, http.StatusNotFound, nil)
			return
		}
		formatted, err := key.FormatSSH("mock-parent")
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		reply(w, http.StatusOK, contract.PublicKeyResponse{PublicKey: formatted})
	})

	// Login. The mock authenticates nobody: any credentials are accepted, and
	// the token handed back is a badge on whatever organization exists. QA
	// asserts on plakman's behavior, not on core-api's auth.
	mux.HandleFunc("POST /api/v1/auth/login/password", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		token, err := p.badge(p.anyOrg())
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		reply(w, http.StatusOK, contract.LoginResponse{Token: token})
	})

	// Email + poll: the UI-driven bootstrap login. Resolves immediately here,
	// because there is no mailbox to click through.
	//
	// core-api gates the poll on a completion code, shown to whoever completed
	// the sign-in, and plakman refuses a parent that does not. A wrong code is
	// refused; a missing one is not, because plakman's own suite polls without.
	mux.HandleFunc("POST /api/v1/auth/login/email", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		id := uuid.NewString()
		p.polls[id] = uuid.NewString()[:8]
		reply(w, http.StatusOK, struct {
			PollId         string `json:"poll_id"`
			CompletionCode bool   `json:"completion_code"`
		}{PollId: id, CompletionCode: true})
	})
	mux.HandleFunc("POST /api/v1/auth/poll/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		code, found := p.polls[r.PathValue("id")]
		if !found {
			reply(w, http.StatusNotFound, nil)
			return
		}
		if got := r.Header.Get("X-Completion-Code"); got != "" && got != code {
			reply(w, http.StatusUnauthorized, struct {
				Code string `json:"code"`
			}{"completion_code_required"})
			return
		}
		token, err := p.badge(p.anyOrg())
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		reply(w, http.StatusOK, contract.TokenResponse{Token: token})
	})
	// Not core-api: what the sign-in page would have shown, for the rig to read.
	mux.HandleFunc("GET /qa/completion-code/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		code, found := p.polls[r.PathValue("id")]
		if !found {
			reply(w, http.StatusNotFound, nil)
			return
		}
		reply(w, http.StatusOK, struct {
			CompletionCode string `json:"completion_code"`
		}{code})
	})

	mux.HandleFunc("GET /api/v1/account/me", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		reply(w, http.StatusOK, contract.AccountMeResponse{
			UserId:         p.user,
			OrganizationId: p.anyOrg(),
			Name:           "Mock Parent Admin",
			Email:          "test@plakar.io",
			Account:        "email/test@plakar.io",
		})
	})

	mux.HandleFunc("GET /api/v1/account/organizations", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		out := make([]contract.OrganizationResponse, 0, len(p.orgs))
		for _, o := range p.orgs {
			out = append(out, o)
		}
		reply(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/v1/account/organizations", func(w http.ResponseWriter, r *http.Request) {
		var req contract.OrganizationCreateRequest
		if !decode(r, &req) {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		now := time.Now().UTC()
		org := contract.OrganizationResponse{
			Id:        uuid.New(),
			Revision:  0,
			Name:      req.Name,
			Info:      req.Info,
			Type:      req.Type,
			CreatedAt: now,
			UpdatedAt: now,
		}
		p.orgs[org.Id] = org
		log.Printf("created organization %s (%s, %s)", org.Name, org.Id, org.Type)
		reply(w, http.StatusOK, org)
	})

	mux.HandleFunc("GET /api/v1/account/organizations/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		org, ok := p.orgs[id]
		if !ok {
			reply(w, http.StatusNotFound, nil)
			return
		}
		reply(w, http.StatusOK, org)
	})

	mux.HandleFunc("POST /api/v1/auth/badge", func(w http.ResponseWriter, r *http.Request) {
		var req contract.BadgeRequest
		if !decode(r, &req) {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		org := req.OrganizationId
		if org == uuid.Nil {
			org = p.anyOrg()
		}
		token, err := p.badge(org)
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		reply(w, http.StatusOK, contract.TokenResponse{Token: token})
	})

	// Licensing. plakman signs the challenge with its own key and sends the
	// public half; the mock does not verify that signature -- core-api's job is
	// to decide who gets a license, and QA is not testing that decision.
	mux.HandleFunc("GET /api/v1/licensing/challenge", func(w http.ResponseWriter, r *http.Request) {
		ch := credentials.NewChallenge(5 * time.Minute)
		signed, _, err := p.jwt.SignedString(ch)
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		reply(w, http.StatusOK, contract.ChallengeResponse{Challenge: signed})
	})

	mux.HandleFunc("POST /api/v1/licensing/enroll", func(w http.ResponseWriter, r *http.Request) {
		var req contract.EnrollRequest
		if !decode(r, &req) {
			reply(w, http.StatusBadRequest, nil)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		now := time.Now().Truncate(time.Second)
		lic := credentials.License{
			IssuedAt:       now,
			IdentityId:     uuid.New(),
			OrganizationId: p.anyOrg(),
			Plan:           "enterprise",
			Mode:           credentials.ModeOnline,
			QuotaSoft:      credentials.DefaultQuotaSoft,
			QuotaHard:      credentials.DefaultQuotaHard,
		}
		signed, _, err := p.jwt.SignedString(&lic)
		if err != nil {
			reply(w, http.StatusInternalServerError, nil)
			return
		}
		log.Printf("issued license identity=%s org=%s", lic.IdentityId, lic.OrganizationId)
		reply(w, http.StatusOK, contract.LicenseResponse{License: signed})
	})

	// Anything unmapped is a gap in the mock, not a 404 to shrug at: log it
	// loudly so the missing endpoint shows up the first time it is needed.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern == "" {
			log.Printf("UNHANDLED %s %s", r.Method, r.URL.Path)
		}
		mux.ServeHTTP(w, r)
	})

	log.Printf("mock-parent listening on %s (kid=%s)", *addr, keyId)
	log.Fatal(http.ListenAndServe(*addr, handler))
}

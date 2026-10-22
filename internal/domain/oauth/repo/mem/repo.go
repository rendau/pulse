package mem

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

// Repo — oauth-репозиторий в памяти: тесты гоняют на нём настоящий доменный сервис.
type Repo struct {
	mu     sync.Mutex
	grants map[string]*model.Grant
	tokens map[string]*model.Token
	seq    int
}

func New() *Repo {
	return &Repo{grants: map[string]*model.Grant{}, tokens: map[string]*model.Token{}}
}

func (r *Repo) GetGrant(_ context.Context, id string) (*model.Grant, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.grants[id]
	if !ok {
		return nil, false, nil
	}
	return new(*g), true, nil
}

func (r *Repo) CreateGrant(_ context.Context, obj *model.Grant) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	g := new(*obj)
	g.Id = "grant-" + strconv.Itoa(r.seq)
	g.CreatedAt = time.Now()
	r.grants[g.Id] = g
	return g.Id, nil
}

func (r *Repo) DeleteGrant(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.grants, id)
	for hash, t := range r.tokens {
		if t.GrantId == id {
			delete(r.tokens, hash)
		}
	}
	return nil
}

func (r *Repo) MarkGrantConnected(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.grants[id]; ok && g.ConnectedAt == nil {
		g.ConnectedAt = new(time.Now())
	}
	return nil
}

func (r *Repo) TouchGrantLastUsed(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.grants[id]; ok {
		g.LastUsedAt = new(time.Now())
	}
	return nil
}

func (r *Repo) CreateTokens(_ context.Context, tokens []*model.Token) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tokens {
		r.tokens[t.Hash] = new(*t)
	}
	return nil
}

func (r *Repo) GetToken(_ context.Context, hash string) (*model.Token, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[hash]
	if !ok {
		return nil, false, nil
	}
	return new(*t), true, nil
}

func (r *Repo) TakeToken(_ context.Context, hash, kind string) (*model.Token, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tokens[hash]
	if !ok || t.Kind != kind {
		return nil, false, nil
	}
	delete(r.tokens, hash)
	return t, true, nil
}

func (r *Repo) Cleanup(_ context.Context, _ time.Time) (int64, int64, error) {
	return 0, 0, nil
}

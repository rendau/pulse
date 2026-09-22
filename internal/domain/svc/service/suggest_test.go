package service

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/domain/svc/model"
)

type fakeRepo struct{ items []*model.Main }

func (f *fakeRepo) List(context.Context, *model.ListReq) ([]*model.Main, int64, error) {
	return f.items, int64(len(f.items)), nil
}

func (f *fakeRepo) Get(_ context.Context, name string) (*model.Main, bool, error) {
	item, ok := lo.Find(f.items, func(v *model.Main) bool { return v.Name == name })
	return item, ok, nil
}

func (f *fakeRepo) UpdateOrCreate(context.Context, *model.Edit) error        { return nil }
func (f *fakeRepo) Delete(context.Context, string) error                     { return nil }
func (f *fakeRepo) DeleteStale(context.Context, time.Time) ([]string, error) { return nil, nil }

func TestGetOrSuggest_ClusterName(t *testing.T) {
	s := New(&fakeRepo{items: catalog()})

	svc, err := s.GetOrSuggest(context.Background(), "ocenter")
	require.NoError(t, err, "однозначное имя в кластере принимается вместо имени каталога")
	assert.Equal(t, "orders-center", svc.Name)

	_, err = s.GetOrSuggest(context.Background(), "paymets-api")
	assert.ErrorContains(t, err, "similar: payments-api", "опечатка — подсказка, а не молчаливая подмена")
}

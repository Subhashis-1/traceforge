package otel

//go:generate mockgen -destination=../storage/mock_repository_gomock_test.go -package=storage github.com/Subhashis-1/traceforge/internal/storage Repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Subhashis-1/traceforge/internal/models"
)

type mockRepository struct {
	ctrl     *gomock.Controller
	recorder *mockRepositoryRecorder
}

type mockRepositoryRecorder struct {
	mock *mockRepository
}

func newMockRepository(ctrl *gomock.Controller) *mockRepository {
	mock := &mockRepository{ctrl: ctrl}
	mock.recorder = &mockRepositoryRecorder{mock: mock}
	return mock
}

func (m *mockRepository) EXPECT() *mockRepositoryRecorder {
	return m.recorder
}

func (m *mockRepository) CreateSessionTraceMap(ctx context.Context, sessionID, traceID uuid.UUID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateSessionTraceMap", ctx, sessionID, traceID)
	ret0, _ := ret[0].(error)
	return ret0
}

func (mr *mockRepositoryRecorder) CreateSessionTraceMap(ctx, sessionID, traceID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateSessionTraceMap", reflect.TypeOf((*mockRepository)(nil).CreateSessionTraceMap), ctx, sessionID, traceID)
}

func (m *mockRepository) CreateTrace(context.Context, *models.Trace) error { panic("unexpected call") }
func (m *mockRepository) GetTraceByID(context.Context, uuid.UUID) (*models.Trace, error) {
	panic("unexpected call")
}
func (m *mockRepository) ListTraces(context.Context, string, time.Time, time.Time, int, []byte) ([]*models.Trace, []byte, error) {
	panic("unexpected call")
}
func (m *mockRepository) CreateSpan(context.Context, *models.Span) error { panic("unexpected call") }
func (m *mockRepository) CreateSpanBatch(context.Context, []*models.Span) error {
	panic("unexpected call")
}
func (m *mockRepository) ListSpansByTrace(context.Context, uuid.UUID) ([]*models.Span, error) {
	panic("unexpected call")
}
func (m *mockRepository) CreateEvent(context.Context, *models.Event) error { panic("unexpected call") }
func (m *mockRepository) CreateSessionEvent(context.Context, *models.Event) error {
	panic("unexpected call")
}
func (m *mockRepository) ListEventsBySession(context.Context, uuid.UUID, time.Time, time.Time, int) ([]*models.Event, error) {
	panic("unexpected call")
}
func (m *mockRepository) GetTraceIDBySession(context.Context, uuid.UUID) (uuid.UUID, error) {
	panic("unexpected call")
}
func (m *mockRepository) CreateTraceBlob(context.Context, uuid.UUID, []byte) error {
	panic("unexpected call")
}
func (m *mockRepository) GetTraceBlob(context.Context, uuid.UUID) ([]byte, error) {
	panic("unexpected call")
}
func (m *mockRepository) HealthCheck(context.Context) error { panic("unexpected call") }
func (m *mockRepository) SearchTraces(context.Context, *models.Query, int) ([]*models.Trace, error) {
	panic("unexpected call")
}

func TestCorrelationMiddleware(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := newMockRepository(ctrl)
	sessionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	traceID := uuid.MustParse("44444444-4444-4444-4444-444444444444")

	repo.EXPECT().
		CreateSessionTraceMap(gomock.Any(), sessionID, traceID).
		Times(1).
		Return(nil)

	handler := CorrelationMiddleware(repo)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	body := `{
		"resourceSpans":[
			{
				"instrumentationLibrarySpans":[
					{
						"spans":[
							{"traceId":"44444444-4444-4444-4444-444444444444"}
						]
					}
				]
			}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Session-Id", sessionID.String())
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok", rec.Body.String())
}

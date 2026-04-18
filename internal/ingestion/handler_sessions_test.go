package ingestion

//go:generate mockgen -destination=../storage/mock_repository_gomock_test.go -package=storage github.com/Subhashis-1/traceforge/internal/storage Repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
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

func (m *mockRepository) CreateSessionEvent(ctx context.Context, ev *models.Event) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateSessionEvent", ctx, ev)
	ret0, _ := ret[0].(error)
	return ret0
}

func (mr *mockRepositoryRecorder) CreateSessionEvent(ctx, ev interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateSessionEvent", reflect.TypeOf((*mockRepository)(nil).CreateSessionEvent), ctx, ev)
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
func (m *mockRepository) ListEventsBySession(context.Context, uuid.UUID, time.Time, time.Time, int) ([]*models.Event, error) {
	panic("unexpected call")
}
func (m *mockRepository) GetTraceIDBySession(context.Context, uuid.UUID) (uuid.UUID, error) {
	panic("unexpected call")
}
func (m *mockRepository) CreateSessionTraceMap(context.Context, uuid.UUID, uuid.UUID) error {
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
func (m *mockRepository) GetLatencyMetrics(context.Context, string, time.Time) ([]*models.LatencyMetric, error) {
	panic("unexpected call")
}

func TestHandleSessionBatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repo := newMockRepository(ctrl)
	sessionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	repo.EXPECT().
		CreateSessionEvent(gomock.Any(), gomock.AssignableToTypeOf(&models.Event{})).
		Times(2).
		DoAndReturn(func(_ context.Context, ev *models.Event) error {
			require.Equal(t, sessionID, ev.SessionID)
			require.NotEqual(t, uuid.Nil, ev.EventID)
			require.False(t, ev.Timestamp.IsZero())
			return nil
		})

	e := echo.New()
	e.POST("/v1/sessions", NewSessionHandler(repo))

	body := `{
		"sessionId":"11111111-1111-1111-1111-111111111111",
		"events":[
			{"eventId":"22222222-2222-2222-2222-222222222222","ts":1700000000000,"type":"click"},
			{"eventId":"33333333-3333-3333-3333-333333333333","ts":1700000001000,"type":"input"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Status    string `json:"status"`
		Processed int    `json:"processed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "ok", resp.Status)
	require.Equal(t, 2, resp.Processed)
}

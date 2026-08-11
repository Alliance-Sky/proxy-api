package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Alliance-Sky/proxy-api/internal/api"
	"github.com/Alliance-Sky/proxy-api/internal/cache"
	"github.com/Alliance-Sky/proxy-api/internal/db"
	"github.com/go-chi/chi/v5"
)

type mockDB struct{}

func (m *mockDB) GetMonths(ctx context.Context) ([]string, error) {
	return []string{"2024-06", "2024-05"}, nil
}

func (m *mockDB) GetFormatsByMonth(ctx context.Context, month string) ([]db.FormatRating, error) {
	return []db.FormatRating{
		{Format: "gen9randombattle", Rating: "1000"},
		{Format: "gen9ou", Rating: "0"},
		{Format: "gen9ou", Rating: "1500"},
		{Format: "gen9ubers", Rating: "0"},
		{Format: "gen8ou", Rating: "0"},
	}, nil
}

func (m *mockDB) GetUsage(ctx context.Context, month, format, rating string) ([]db.UsageRow, error) {
	return []db.UsageRow{
		{Pokemon: "Terapagos", UsagePercent: 25.0},
		{Pokemon: "Great Tusk", UsagePercent: 12.5},
	}, nil
}

func (m *mockDB) GetViability(ctx context.Context, month, format, rating string) ([]db.ViabilityRow, error) {
	return []db.ViabilityRow{
		{Pokemon: "Terapagos", Viability: []byte(`{"ce":85}`)},
	}, nil
}

func (m *mockDB) GetLeads(ctx context.Context, month, format, rating string) ([]db.LeadRow, error) {
	return []db.LeadRow{
		{Pokemon: "Terapagos", LeadPercent: 5.0},
	}, nil
}

func (m *mockDB) GetAllFormatRatings(ctx context.Context) ([]db.MonthFormatRating, error) {
	return nil, nil
}

func (m *mockDB) GetMetagame(ctx context.Context, month, format, rating string) (db.MetagameRow, error) {
	return db.MetagameRow{Stalliness: 0.05, Playstyles: []byte(`{"balance":0.4}`)}, nil
}

func (m *mockDB) GetFormatStats(ctx context.Context, month, format, rating string) (int, error) {
	return 123456, nil
}

func (m *mockDB) GetTrend(ctx context.Context, format, rating string, pokemons []string, months []string) ([]db.TrendRow, error) {
	return nil, nil
}

func setupTestRouter() *chi.Mux {
	cacheSvc, _ := cache.NewService()
	h := api.NewHandler(cacheSvc, &mockDB{}, "", "")

	r := chi.NewRouter()
	h.RegisterRoutes(r)
	return r
}

func TestGetInit(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/init", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// 1. Check formats are sorted correctly (gen9 before gen8, ou before ubers before randombattle)
	formatsRaw, ok := response["formats"].([]interface{})
	if !ok {
		t.Fatalf("formats is not an array")
	}

	if len(formatsRaw) != 4 {
		t.Fatalf("expected 4 format items, got %d", len(formatsRaw))
	}

	f1 := formatsRaw[0].(map[string]interface{})
	if f1["format"] != "gen9ou" {
		t.Errorf("Expected first format to be gen9ou, got %s", f1["format"])
	}
	f2 := formatsRaw[1].(map[string]interface{})
	if f2["format"] != "gen9ubers" {
		t.Errorf("Expected second format to be gen9ubers, got %s", f2["format"])
	}
	f3 := formatsRaw[2].(map[string]interface{})
	if f3["format"] != "gen9randombattle" {
		t.Errorf("Expected third format to be gen9randombattle, got %s", f3["format"])
	}
	f4 := formatsRaw[3].(map[string]interface{})
	if f4["format"] != "gen8ou" {
		t.Errorf("Expected fourth format to be gen8ou, got %s", f4["format"])
	}

	// 2. Check stats structure (array of tuples)
	statsRaw, ok := response["stats"].([]interface{})
	if !ok {
		t.Fatalf("stats is not an array")
	}

	if len(statsRaw) == 0 {
		t.Fatalf("expected non-empty stats array")
	}

	firstStat, ok := statsRaw[0].([]interface{})
	if !ok {
		t.Fatalf("stats item is not a tuple (array). Frontend will break!")
	}

	if len(firstStat) != 5 {
		t.Fatalf("expected tuple of length 5, got %d", len(firstStat))
	}

	rank := firstStat[0].(float64) // JSON numbers parse as float64
	pokemon := firstStat[1].(string)

	if rank != 1 || pokemon != "Terapagos" {
		t.Errorf("Expected [1, 'Terapagos', ...], got [%v, %v, ...]", rank, pokemon)
	}
}

func TestGetAggregatedStatsTuple(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/stats?month=2024-06&format=gen9ou&rating=1500", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var statsRaw []interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &statsRaw); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(statsRaw) == 0 {
		t.Fatalf("expected non-empty stats array")
	}

	firstStat, ok := statsRaw[0].([]interface{})
	if !ok {
		t.Fatalf("stats item is not a tuple (array). Frontend will break!")
	}

	if len(firstStat) != 5 {
		t.Fatalf("expected tuple of length 5, got %d", len(firstStat))
	}
}

func TestGetMonths(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/months", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var months []string
	if err := json.Unmarshal(rr.Body.Bytes(), &months); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(months) != 2 || months[0] != "2024-06" || months[1] != "2024-05" {
		t.Errorf("Expected ['2024-06', '2024-05'], got %v", months)
	}
}

func TestGetFormats(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/formats?month=2024-06", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var formats []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &formats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(formats) != 4 {
		t.Fatalf("expected 4 formats, got %d", len(formats))
	}

	if formats[0]["format"] != "gen9ou" {
		t.Errorf("Expected first format to be gen9ou, got %v", formats[0]["format"])
	}
}

func TestGetUsage(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/usage?month=2024-06&format=gen9ou&rating=1500", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var usage []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &usage); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(usage) == 0 {
		t.Fatalf("Expected non-empty usage array")
	}

	if usage[0]["pokemon"] != "Terapagos" || usage[0]["usagePercent"] != "25.00000%" {
		t.Errorf("Expected Terapagos 25.00000%%, got %v", usage[0])
	}
}

func TestGetMetagame(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/metagame?month=2024-06&format=gen9ou&rating=1500", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var metagame map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &metagame); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if metagame["stalliness"] != 0.05 {
		t.Errorf("Expected stalliness 0.05, got %v", metagame["stalliness"])
	}
}

func TestGetFormatStats(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/format-stats?month=2024-06&format=gen9ou&rating=1500", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if stats["totalBattles"] != float64(123456) {
		t.Errorf("Expected totalBattles 123456, got %v", stats["totalBattles"])
	}
}

func TestGetTrend(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/trend?format=gen9ou&rating=1500&pokemon=Terapagos", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}

func TestGetInitRigorous(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/init", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// 1. Verify Top-Level Keys
	expectedKeys := []string{"months", "formats", "defaultMonth", "defaultFormat", "defaultRating", "stats", "metagame", "totalBattles"}
	for _, key := range expectedKeys {
		if _, ok := response[key]; !ok {
			t.Errorf("Expected top-level key '%s' in init response, but it was missing", key)
		}
	}

	// 2. Verify 'months' is an array of strings
	monthsRaw, ok := response["months"].([]interface{})
	if !ok {
		t.Fatalf("'months' is not an array")
	}
	if len(monthsRaw) == 0 {
		t.Fatalf("'months' array is empty")
	}
	if _, ok := monthsRaw[0].(string); !ok {
		t.Errorf("'months' array does not contain strings")
	}

	// 3. Verify 'defaultMonth', 'defaultFormat', 'defaultRating' are strings
	if _, ok := response["defaultMonth"].(string); !ok {
		t.Errorf("'defaultMonth' is not a string")
	}
	if _, ok := response["defaultFormat"].(string); !ok {
		t.Errorf("'defaultFormat' is not a string")
	}
	if _, ok := response["defaultRating"].(string); !ok {
		t.Errorf("'defaultRating' is not a string")
	}

	// 4. Verify 'totalBattles' is a number
	if _, ok := response["totalBattles"].(float64); !ok {
		t.Errorf("'totalBattles' is not a number")
	}

	// 5. Verify 'metagame' is an object with 'stalliness' and 'playstyles'
	metaRaw, ok := response["metagame"].(map[string]interface{})
	if !ok {
		t.Fatalf("'metagame' is not an object")
	}
	if _, ok := metaRaw["stalliness"].(float64); !ok {
		t.Errorf("'metagame.stalliness' is not a number")
	}
	if _, ok := metaRaw["playstyles"].(map[string]interface{}); !ok {
		t.Errorf("'metagame.playstyles' is not an object")
	}

	// 6. Verify 'formats' shape
	formatsRaw, ok := response["formats"].([]interface{})
	if !ok {
		t.Fatalf("'formats' is not an array")
	}
	format1, ok := formatsRaw[0].(map[string]interface{})
	if !ok {
		t.Fatalf("'formats' item is not an object")
	}
	if _, ok := format1["format"].(string); !ok {
		t.Errorf("'formats[0].format' is not a string")
	}
	ratingsRaw, ok := format1["ratings"].([]interface{})
	if !ok {
		t.Fatalf("'formats[0].ratings' is not an array")
	}
	if _, ok := ratingsRaw[0].(string); !ok {
		t.Errorf("'formats[0].ratings' does not contain strings")
	}

	// 7. Verify 'stats' shape (array of tuples)
	statsRaw, ok := response["stats"].([]interface{})
	if !ok {
		t.Fatalf("'stats' is not an array")
	}
	stat1, ok := statsRaw[0].([]interface{})
	if !ok {
		t.Fatalf("'stats' item is not an array (tuple). Frontend will break!")
	}
	if len(stat1) != 5 {
		t.Fatalf("expected 'stats' tuple of length 5, got %d", len(stat1))
	}
	if _, ok := stat1[0].(float64); !ok {
		t.Errorf("Tuple index 0 (rank) is not a number")
	}
	if _, ok := stat1[1].(string); !ok {
		t.Errorf("Tuple index 1 (pokemon) is not a string")
	}
	if _, ok := stat1[2].(float64); !ok {
		t.Errorf("Tuple index 2 (usagePercent) is not a number")
	}
	if _, ok := stat1[3].(float64); !ok {
		t.Errorf("Tuple index 3 (leadPercent) is not a number")
	}
	// index 4 is viability, which is raw json/map, skip strict type check
}

func TestGetTrendRigorous(t *testing.T) {
	r := setupTestRouter()
	req := httptest.NewRequest("GET", "/api/v3/trend?format=gen9ou&rating=1500&pokemon=Terapagos", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Trend returns an object keyed by pokemon name, containing array of {month, usagePercent}
	terapagosData, ok := response["Terapagos"].([]interface{})
	if !ok {
		t.Fatalf("Expected key 'Terapagos' to be an array")
	}

	if len(terapagosData) > 0 {
		item, ok := terapagosData[0].(map[string]interface{})
		if !ok {
			t.Fatalf("Trend array item is not an object")
		}
		if _, ok := item["month"].(string); !ok {
			t.Errorf("Trend item 'month' is not a string")
		}
		if _, ok := item["usagePercent"].(float64); !ok {
			t.Errorf("Trend item 'usagePercent' is not a number")
		}
	}
}

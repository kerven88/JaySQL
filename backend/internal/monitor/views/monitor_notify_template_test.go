package views

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/sirupsen/logrus"
)

func TestNotifyTemplatePreviewEndpoint(t *testing.T) {
	oldLog := global.App.Log
	global.App.Log = logrus.New()
	t.Cleanup(func() { global.App.Log = oldLog })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/preview", PreviewNotifyMessageTemplateView)
	for _, tc := range []struct {
		body   string
		wantOK bool
	}{
		{`{"template":"**{{instance_name}}**: {{threshold}}","trigger":"firing"}`, true},
		{`{"template":"{{no_such_variable}}"}`, false},
		{`{"template":"{{title}}","trigger":"unknown"}`, false},
		{`{`, false},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/preview", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		var body struct {
			Code string `json:"code"`
			Data struct {
				PlainText    string `json:"plain_text"`
				TelegramHTML string `json:"telegram_html"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if (body.Code == "0000") != tc.wantOK {
			t.Fatalf("%s: %s", tc.body, w.Body.String())
		}
		if tc.wantOK && (!strings.Contains(body.Data.PlainText, "dev-mysql") || !strings.Contains(body.Data.TelegramHTML, "<b>")) {
			t.Fatal(w.Body.String())
		}
	}
}

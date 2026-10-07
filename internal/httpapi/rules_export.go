// Экспорт правил (чанк 52): POST /rules/export?format=text|stix|dataset.
package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// stixIndicator — индикатор STIX 2.1 bundle (экспорт правил).
type stixIndicator struct {
	Type        string `json:"type"`
	SpecVersion string `json:"spec_version"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Pattern     string `json:"pattern"`
	PatternType string `json:"pattern_type"`
	ValidFrom   string `json:"valid_from"`
}

// exportRules — POST /rules/export?format=...:
//   - text: .rules-файл из выбранных фильтром правил (raw, по одному на
//     строку, в порядке сборки);
//   - stix: STIX 2.1 bundle, каждое правило — indicator с
//     pattern_type="suricata" (pattern = raw; id детерминирован по sid);
//   - dataset: активные IOC в формате Suricata Dataset (type,value;
//     ip→ipv4/ipv6, domain→dns, md5/sha256 → как есть, прочие → string).
//
// Формат ответа — файл (Content-Disposition attachment).
func (h *handlers) exportRules(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.resolveOrgID(w, r)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "text" && format != "stix" && format != "dataset" {
		writeValidation(w, fieldErrors{"format": "text|stix|dataset"})
		return
	}
	var in struct {
		RuleFilter ruleFilterInput `json:"rule_filter"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}

	if format == "dataset" {
		h.exportDataset(w, r, orgID)
		return
	}

	filter := store.RuleFilter{
		Status:   in.RuleFilter.Status,
		Category: in.RuleFilter.Category,
		Tag:      in.RuleFilter.Tag,
		Source:   in.RuleFilter.Source,
		SID:      in.RuleFilter.Sid,
	}
	if filter.Status == "" {
		filter.Status = "enabled"
	}
	raw, err := h.d.Store.Rules.SelectRawForBuild(r.Context(), orgID, filter)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"по фильтру не выбрано ни одного правила", nil)
		return
	}

	switch format {
	case "text":
		var b strings.Builder
		for _, rr := range raw {
			b.WriteString(rr.Raw)
			b.WriteString("\n")
		}
		attach(w, "surifleet-rules.rules", "text/plain; charset=utf-8", []byte(b.String()))
	case "stix":
		now := time.Now().UTC().Format(time.RFC3339)
		objects := make([]stixIndicator, 0, len(raw))
		for _, rr := range raw {
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("surifleet-rule:%d", rr.SID)))
			objects = append(objects, stixIndicator{
				Type: "indicator", SpecVersion: "2.1",
				ID: "indicator--" + id.String(),
				Pattern: rr.Raw, PatternType: "suricata", ValidFrom: now,
			})
		}
		bundle := map[string]any{
			"type":         "bundle",
			"id":           "bundle--" + uuid.New().String(),
			"spec_version": "2.1",
			"objects":      objects,
		}
		body, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			writeStoreError(w, err)
			return
		}
		attach(w, "surifleet-rules.stix.json", "application/stix+json", body)
	}
}

// exportDataset — активные IOC в формате Suricata Dataset.
func (h *handlers) exportDataset(w http.ResponseWriter, r *http.Request, orgID uuid.UUID) {
	iocs, err := h.d.Store.Iocs.ListActiveForGeneration(r.Context(), orgID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(iocs) == 0 {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"нет активных IOC для dataset", nil)
		return
	}
	var b strings.Builder
	for _, ioc := range iocs {
		t := "string"
		switch ioc.Type {
		case "ip":
			if strings.Contains(ioc.Value, ":") {
				t = "ipv6"
			} else {
				t = "ipv4"
			}
		case "domain":
			t = "dns"
		case "md5":
			t = "md5"
		case "sha256":
			t = "sha256"
		}
		fmt.Fprintf(&b, "%s,%s\n", t, ioc.Value)
	}
	attach(w, "surifleet-dataset.lst", "text/plain; charset=utf-8", []byte(b.String()))
}

// attach — ответ-файл с Content-Disposition.
func attach(w http.ResponseWriter, filename, contentType string, body []byte) {
	sum := sha256.Sum256(body)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sum[:8]))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

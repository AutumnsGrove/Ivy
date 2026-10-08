package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/AutumnsGrove/Ivy/api"
	"github.com/AutumnsGrove/Ivy/llm"
	"github.com/AutumnsGrove/Ivy/store"
)

// maxSmartBodyBytes bounds a settings patch: a few caps, a few switches and a few
// model ids. Above it the request is refused as malformed.
const maxSmartBodyBytes = 8 << 10

// SmartControls is what the Smart features screen reads and writes. The gate
// implements it, so the screen shows the values admission applies and a saved value
// is one admission will honour. The Check functions of package llm let a request be
// validated whole before any of it is written.
type SmartControls interface {
	SpendCaps
	FeatureEnabled(ctx context.Context, accountID, feature string) bool
	ChosenChatModel(ctx context.Context) string
	ChosenFeatureModel(ctx context.Context, feature string) string
	SetGlobalCap(ctx context.Context, usd float64) error
	SetAccountCap(ctx context.Context, accountID string, usd float64) error
	SetFeatureEnabled(ctx context.Context, accountID, feature string, on bool) error
	SetChatModel(ctx context.Context, id string) error
	SetFeatureModel(ctx context.Context, feature, id string) error
}

// WithSmartControls gives the Smart features screen the gate. The same gate answers
// the spend endpoints' caps, so the two screens cannot disagree.
func (s *Server) WithSmartControls(c SmartControls) *Server {
	s.smart = c
	s.spendCaps = c
	return s
}

// controls is the gate the screen talks to. A server built without one (a test, or
// a build that never wired the LLM layer) gets a gate with no providers: it can
// read and write settings and can reach nothing.
func (s *Server) controls() SmartControls {
	if s.smart != nil {
		return s.smart
	}
	return llm.NewGate(s.dbs)
}

func (s *Server) handleGetSmart(w http.ResponseWriter, r *http.Request) {
	out, err := s.smartSettings(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) smartSettings(r *http.Request) (api.SmartSettings, error) {
	ctx := r.Context()
	ctl := s.controls()
	views, err := s.accountViews(r)
	if err != nil {
		return api.SmartSettings{}, err
	}
	period := store.Period(s.now())
	month, err := s.dbs.GlobalSpend(ctx, period)
	if err != nil {
		return api.SmartSettings{}, err
	}
	listed := llm.UserFeatures()

	out := api.SmartSettings{
		GlobalCapUsd: ctl.GlobalCapUSD(ctx), GlobalMonthUsd: month.USD,
		Accounts:      make([]api.SmartAccount, 0, len(views)),
		Features:      make([]api.SmartFeature, 0, len(listed)),
		Models:        []api.SmartModel{},
		ChatModel:     ctl.ChosenChatModel(ctx),
		FeatureModels: map[string]string{},
	}
	for _, f := range listed {
		out.Features = append(out.Features, api.SmartFeature{Name: f.Name, Label: f.Label, DefaultOn: f.DefaultOn})
	}
	for _, v := range views {
		spent, err := s.dbs.AccountSpend(ctx, v.Id, period)
		if err != nil {
			return api.SmartSettings{}, err
		}
		acct := api.SmartAccount{
			Id: v.Id, Address: v.Address, Short: v.Short, Smart: v.Smart,
			CapUsd: ctl.AccountCapUSD(ctx, v.Id), MonthUsd: spent.USD,
			Features: make(map[string]bool, len(listed)),
		}
		for _, f := range listed {
			acct.Features[f.Name] = ctl.FeatureEnabled(ctx, v.Id, f.Name)
		}
		out.Accounts = append(out.Accounts, acct)
	}
	for _, m := range llm.Models() {
		if m.Kind == llm.EndpointChat || m.Kind == llm.EndpointVision {
			out.Models = append(out.Models, api.SmartModel{
				Id: m.ID, Name: m.Name, Multimodal: m.Multimodal, InPerM: m.InPerM, OutPerM: m.OutPerM,
			})
		}
	}
	for _, name := range llm.FeatureNames() {
		if id := ctl.ChosenFeatureModel(ctx, name); id != "" {
			out.FeatureModels[name] = id
		}
	}
	return out, nil
}

func (s *Server) handleUpdateSmart(w http.ResponseWriter, r *http.Request) {
	var body api.SmartSettingsPatch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSmartBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That change is not one Ivy understands")
		return
	}
	ctx := r.Context()
	ctl := s.controls()

	// Everything is checked before anything is written, so a refusal changes
	// nothing. The accounts come first: an unknown one is a 404, not a bad value.
	if body.Accounts != nil {
		known, err := s.accountViews(r)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		have := make(map[string]bool, len(known))
		for _, v := range known {
			have[v.Id] = true
		}
		for id := range *body.Accounts {
			if !have[id] {
				s.notFound(w, r, "account")
				return
			}
		}
	}
	if err := checkSmartPatch(body); err != nil {
		var bad *llm.BadValue
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, "bad_request", bad.Reason)
			return
		}
		s.serverError(w, r, err)
		return
	}

	if err := applySmartPatch(ctx, ctl, body); err != nil {
		s.serverError(w, r, err)
		return
	}
	out, err := s.smartSettings(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func checkSmartPatch(p api.SmartSettingsPatch) error {
	if p.GlobalCapUsd != nil {
		if err := llm.CheckCap(*p.GlobalCapUsd); err != nil {
			return err
		}
	}
	if p.ChatModel != nil {
		if err := llm.CheckChatModel(*p.ChatModel); err != nil {
			return err
		}
	}
	if p.FeatureModels != nil {
		for name, id := range *p.FeatureModels {
			if err := llm.CheckFeatureModel(name, id); err != nil {
				return err
			}
		}
	}
	if p.Accounts != nil {
		for _, a := range *p.Accounts {
			if a.CapUsd != nil {
				if err := llm.CheckCap(*a.CapUsd); err != nil {
					return err
				}
			}
			if a.Features != nil {
				for name := range *a.Features {
					if err := llm.CheckFeature(name); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// applySmartPatch writes a patch that has passed checkSmartPatch, in a fixed order
// so a failure part-way is the same failure every time.
func applySmartPatch(ctx context.Context, ctl SmartControls, p api.SmartSettingsPatch) error {
	if p.GlobalCapUsd != nil {
		if err := ctl.SetGlobalCap(ctx, *p.GlobalCapUsd); err != nil {
			return err
		}
	}
	if p.ChatModel != nil {
		if err := ctl.SetChatModel(ctx, *p.ChatModel); err != nil {
			return err
		}
	}
	if p.FeatureModels != nil {
		for _, name := range sortedKeys(*p.FeatureModels) {
			if err := ctl.SetFeatureModel(ctx, name, (*p.FeatureModels)[name]); err != nil {
				return err
			}
		}
	}
	if p.Accounts != nil {
		for _, id := range sortedKeys(*p.Accounts) {
			a := (*p.Accounts)[id]
			if a.CapUsd != nil {
				if err := ctl.SetAccountCap(ctx, id, *a.CapUsd); err != nil {
					return err
				}
			}
			if a.Features != nil {
				for _, name := range sortedKeys(*a.Features) {
					if err := ctl.SetFeatureEnabled(ctx, id, name, (*a.Features)[name]); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

package jev

import (
	"context"
	"errors"
	"log/slog"

	"github.com/AutumnsGrove/Ivy/store"
)

// CategoryQuestion is the id of the category question.
const CategoryQuestion = "category"

// PlacedCategory is the category the cached answer settles on, when it clears its own
// bar and says something other than the quiet option. It makes no request: it reads the
// answer already kept. Whether that category has a tag is the store's business.
func (e *Engine) PlacedCategory(ctx context.Context, accountID, contentKey string) (string, bool) {
	odds, err := e.Odds(ctx, accountID, contentKey)
	if err != nil {
		slog.WarnContext(ctx, "jev: cannot read the category answer", "account", accountID, "content_key", contentKey, "error", err)
		return "", false
	}
	for _, v := range odds.Answers {
		if v.QuestionID == CategoryQuestion && v.Acts() {
			return v.Choice, true
		}
	}
	return "", false
}

// placeCategory puts the settled category's tag on a message, once: it runs only as the
// worker marks the message done, and a done message is never decided again, so a tag
// the operator removes stays removed. The answer is already kept, so a failure here is
// logged and the message still counts as done.
func (w *Worker) placeCategory(ctx context.Context, account, contentKey string) {
	category, ok := w.engine.PlacedCategory(ctx, account, contentKey)
	if !ok {
		return
	}
	if err := w.store.PlaceCategoryTag(ctx, account, contentKey, category); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.WarnContext(ctx, "jev: could not place the category tag", "account", account, "content_key", contentKey, "category", category, "error", err)
	}
}

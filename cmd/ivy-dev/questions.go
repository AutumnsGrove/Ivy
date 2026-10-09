package main

import "github.com/AutumnsGrove/Ivy/jev"

// devQuestions is the one trivial question the dev stack asks, so the whole layer
// (the registry, the state builder, the cache, the worker and the odds sheet) can be
// exercised against the fake provider while the shipped question file is still empty.
// It is not a feature: it ships nowhere but this binary, and it only ever runs for
// an account that has smart features on and the classify switch turned on.
func devQuestions() []jev.Question {
	return []jev.Question{{
		ID: "dev_is_personal",
		Instructions: "Was this written by a person to the owner, rather than sent automatically? " +
			"Newsletters, receipts, notifications and marketing do not count.",
		Criteria: map[string]string{
			"no":  "sent automatically or in bulk",
			"yes": "a person wrote it to the owner",
		},
		QuietOption: "no",
		Threshold:   0.8,
	}}
}

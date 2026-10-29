package home

import (
	"fmt"
	"strings"
)

// Reason is why a checkout's store is the user's call.
type Reason string

const (
	// ReasonKnown: the checkout's repo_id or origin alone names one store, as
	// in a second clone of a known repository.
	ReasonKnown Reason = "known"
	// ReasonConflict: the checkout's identifiers name more than one store.
	ReasonConflict Reason = "conflict"
	// ReasonReused: the checkout's path belonged to a store whose repo_id or
	// origin contradicts its own.
	ReasonReused Reason = "reused"
	// ReasonCopied: the checkout carries a pointer to a store admitting other
	// checkouts and not this one, as a copied checkout does.
	ReasonCopied Reason = "copied"
	// ReasonSplit: a split layout's code and graph repositories work on
	// different stores.
	ReasonSplit Reason = "split"
)

// Question is a checkout's store left to the user, always about the checkout
// a session runs in. Merge names the stores `agentic-memory merge` would
// join, from first, where one is offered.
type Question struct {
	Checkout string
	Reason   Reason
	Stores   []Candidate
	Remotes  []string
	Merge    []string
}

// Candidate is a store a question offers, as a person tells it apart.
type Candidate struct {
	ID        string
	Name      string
	Checkouts []string
}

func (held *mapping) question(checkout Checkout, reason Reason, ids []string) *Question {
	asked := &Question{Checkout: checkout.Root, Reason: reason}

	if checkout.Origin != "" {
		asked.Remotes = append(asked.Remotes, checkout.Origin)
	}

	asked.Remotes = append(asked.Remotes, checkout.Remotes...)

	for _, id := range ids {
		identity, found := held.store(id)
		if !found {
			continue
		}

		asked.Stores = append(asked.Stores, Candidate{ID: id, Name: identity.Name(), Checkouts: identity.Paths()})
	}

	return asked
}

// Text is the question as a hook and a refused call say it, binary naming
// the command that answers it. Its wording is part of the hooks' contract.
func (asked Question) Text(binary string) string {
	var lines []string

	if asked.Reason == ReasonSplit && len(asked.Merge) == 2 {
		lines = append(lines, fmt.Sprintf("the checkout %s and its graph repository keep their memory in two stores. "+
			"Ask the user whether to keep them apart, `%s bind %s`, or join them, `%s merge %s %s`; "+
			"run either from this checkout.", asked.Checkout, binary, asked.Merge[0], binary, asked.Merge[0], asked.Merge[1]))
	} else {
		lines = append(lines, fmt.Sprintf("no store is bound to the checkout %s yet: %s. "+
			"Ask the user which store it belongs to, then run `%s bind <store>` or `%s bind --new` from this checkout.",
			asked.Checkout, asked.reason(), binary, binary))
	}

	for _, candidate := range asked.Stores {
		line := fmt.Sprintf("  %s: %s", candidate.ID, candidate.Name)
		if len(candidate.Checkouts) > 0 {
			line += "; checkouts " + strings.Join(candidate.Checkouts, ", ")
		}

		lines = append(lines, line)
	}

	if len(asked.Remotes) > 0 {
		lines = append(lines, "  remotes of this checkout: "+strings.Join(asked.Remotes, ", "))
	}

	return strings.Join(lines, "\n")
}

func (asked Question) reason() string {
	switch asked.Reason {
	case ReasonKnown:
		return "it is another checkout of a repository a store already holds"
	case ReasonReused:
		return "its path belonged to a checkout of another repository"
	case ReasonCopied:
		return "it carries the pointer of a store that admits other checkouts"
	}

	return "its identifiers name more than one store"
}

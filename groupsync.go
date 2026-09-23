package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Column of the user table holding the 4-digit room number.
const roomColumn = "room"

var memberRe = regexp.MustCompile(`^(\d{4})\s*-\s*(.*)$`)

type groupWarning struct {
	Group   string
	Synced  bool
	Reasons []string
}

// syncGroups mirrors the member lists of all groups into group_user.
// A group is only written if its alias resolves and every member matches
// exactly one user; otherwise it is left untouched. Groups that were skipped
// or synced with doubtful matches are returned as warnings.
func syncGroups(ctx context.Context, list *List) ([]groupWarning, error) {
	var warnings []groupWarning
	for _, col := range list.Cols {
		for _, g := range col {
			synced, reasons, err := syncGroup(ctx, g)
			if err != nil {
				return warnings, fmt.Errorf("group %q: %w", g.Name, err)
			}
			if len(reasons) > 0 {
				warnings = append(warnings, groupWarning{Group: g.Name, Synced: synced, Reasons: reasons})
			}
		}
	}
	return warnings, nil
}

func syncGroup(ctx context.Context, g Group) (bool, []string, error) {
	alias, _, _ := strings.Cut(strings.TrimSpace(g.Mail), "@")
	if alias == "" {
		return false, []string{"keine E-Mail-Adresse angegeben"}, nil
	}

	var groupID int64
	err := membersDB.QueryRowContext(ctx, "SELECT id FROM `group` WHERE mail_alias = ?", alias).Scan(&groupID)
	if err == sql.ErrNoRows {
		return false, []string{fmt.Sprintf("keine Gruppe mit mail_alias %q gefunden", alias)}, nil
	} else if err != nil {
		return false, nil, err
	}

	type member struct{ room, name string }
	var members []member
	for _, raw := range g.Members {
		// Skip entries with no valid room name or name
		parts := memberRe.FindStringSubmatch(strings.TrimSpace(raw))
		if parts == nil || strings.TrimSpace(parts[2]) == "" {
			continue
		}
		members = append(members, member{room: parts[1], name: strings.TrimSpace(parts[2])})
	}

	var reasons []string
	var userIDs []int64
	blocked := false
	seen := make(map[int64]bool)
	for _, mem := range members {
		id, reason, err := matchUser(ctx, mem.room, mem.name)
		if err != nil {
			return false, nil, err
		}
		if reason != "" {
			reasons = append(reasons, fmt.Sprintf("%s - %s: %s", mem.room, mem.name, reason))
		}
		if id == 0 {
			blocked = true
			continue
		}
		if !seen[id] {
			seen[id] = true
			userIDs = append(userIDs, id)
		}
	}
	if blocked {
		return false, reasons, nil
	}

	return true, reasons, replaceMemberships(ctx, groupID, userIDs)
}

// matchUser finds the user in a room whose first name starts with the given
// name. A room with a single occupant matches even if the name doesn't, but
// with a reason to warn about. An id of 0 means no unambiguous match was found.
func matchUser(ctx context.Context, room, name string) (int64, string, error) {
	rows, err := membersDB.QueryContext(ctx,
		"SELECT id, COALESCE(firstname, '') FROM `user` WHERE "+roomColumn+" = ?",
		room,
	)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()

	type candidate struct {
		id        int64
		firstname string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.firstname); err != nil {
			return 0, "", err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}

	if len(candidates) == 0 {
		return 0, "niemand in diesem Zimmer gefunden", nil
	}

	var matches []int64
	for _, c := range candidates {
		if firstnameMatches(c.firstname, name) {
			matches = append(matches, c.id)
		}
	}
	switch len(matches) {
	case 0:
		if len(candidates) == 1 {
			return candidates[0].id, fmt.Sprintf("Vorname passt nicht zu %q, trotzdem eingetragen", candidates[0].firstname), nil
		}
		return 0, fmt.Sprintf("%d Personen in diesem Zimmer, keine mit passendem Vornamen", len(candidates)), nil
	case 1:
		return matches[0], "", nil
	default:
		return 0, fmt.Sprintf("%d Personen in diesem Zimmer mit passendem Vornamen", len(matches)), nil
	}
}

// firstnameMatches reports whether name is a prefix of firstname or of any
// of its individual names, so "Maria" matches "Anna Maria", "Hans-Maria"
// and "Anna (Maria)".
func firstnameMatches(firstname, name string) bool {
	firstname, name = strings.ToLower(firstname), strings.ToLower(name)
	if name == "" {
		return false
	}
	if strings.HasPrefix(firstname, name) {
		return true
	}
	for _, part := range strings.FieldsFunc(firstname, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if strings.HasPrefix(part, name) {
			return true
		}
	}
	return false
}

// replaceMemberships makes userIDs the exact member set of the group. Rows of
// users that stay in the group are kept so their show flag survives.
func replaceMemberships(ctx context.Context, groupID int64, userIDs []int64) error {
	tx, err := membersDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, "SELECT user_id FROM group_user WHERE group_id = ? FOR UPDATE", groupID)
	if err != nil {
		return err
	}
	existing := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	wanted := make(map[int64]bool)
	for _, id := range userIDs {
		wanted[id] = true
		if !existing[id] {
			if _, err := tx.ExecContext(ctx, "INSERT INTO group_user (group_id, user_id) VALUES (?, ?)", groupID, id); err != nil {
				return err
			}
		}
	}
	for id := range existing {
		if !wanted[id] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM group_user WHERE group_id = ? AND user_id = ?", groupID, id); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func formatWarnings(warnings []groupWarning) string {
	var b strings.Builder
	b.WriteString("Hinweise zur Datenbank:\n")
	for _, w := range warnings {
		status := "nicht aktualisiert"
		if w.Synced {
			status = "aktualisiert, bitte prüfen"
		}
		fmt.Fprintf(&b, "\n%s (%s):\n", w.Group, status)
		for _, r := range w.Reasons {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	return b.String()
}

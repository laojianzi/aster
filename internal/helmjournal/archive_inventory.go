package helmjournal

import (
	"context"
	"io"
	"sort"
	"strings"
)

type journalInventory struct{ active, archived []string }

// inventoryJournal shares active quotas with the existing admission path. The
// independent retained-archive budget is also bounded. Archived WALs are fully
// parsed and must satisfy the same terminal/resolved predicate before exclusion
// from active quota; renaming Unknown by hand cannot bypass the guard.
func (s *Store) inventoryJournal(ctx context.Context) (journalInventory, Admission, error) {
	inv := journalInventory{}
	out := Admission{Reason: AdmissionUnavailable}
	if reason, err := admissionContext(ctx); err != nil {
		out.Reason = reason
		return inv, out, err
	}
	dir, err := s.root.root.Open(".")
	if err != nil {
		return inv, out, ErrUnavailable
	}
	entries, readErr := dir.ReadDir(MaxRecords + MaxArchivedRecords + 2)
	closeErr := dir.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil {
		return inv, out, ErrUnavailable
	}
	if len(entries) > MaxRecords+MaxArchivedRecords+1 {
		out.Reason = AdmissionRecordLimit
		return inv, out, ErrBudget
	}
	var activeBytes, archivedBytes int64
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if reason, err := admissionContext(ctx); err != nil {
			out.Reason = reason
			return inv, out, err
		}
		if entry.Name() == lockName {
			continue
		}
		id, ok := recordID(entry.Name())
		archived := false
		if !ok {
			id, archived = strings.CutSuffix(entry.Name(), archivedSuffix)
			ok = archived && validHex(id, 32)
		}
		if !ok || entry.Type() != 0 || seen[id] {
			out.Reason = AdmissionCorrupt
			return inv, out, ErrCorrupt
		}
		seen[id] = true
		if archived {
			inv.archived = append(inv.archived, id)
		} else {
			inv.active = append(inv.active, id)
		}
		if len(inv.active) > MaxRecords {
			out.Reason = AdmissionRecordLimit
			return inv, out, ErrBudget
		}
		if len(inv.archived) > MaxArchivedRecords {
			out.Reason = AdmissionArchiveLimit
			return inv, out, ErrBudget
		}
		f, openErr := s.root.openFile(entry.Name(), false, false)
		if openErr != nil {
			out.RecordID = id
			return inv, out, openErr
		}
		info, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil || closeErr != nil {
			out.RecordID = id
			return inv, out, ErrUnavailable
		}
		if info.Size() > MaxOperationBytes {
			out.Reason, out.RecordID = AdmissionOperationBytes, id
			return inv, out, ErrBudget
		}
		if archived {
			archivedBytes += info.Size()
			if archivedBytes > MaxArchivedBytes {
				out.Reason = AdmissionArchiveLimit
				return inv, out, ErrBudget
			}
		} else {
			activeBytes += info.Size()
			if activeBytes > MaxStoreBytes {
				out.Reason = AdmissionStoreBytes
				return inv, out, ErrBudget
			}
		}
	}
	sort.Strings(inv.active)
	sort.Strings(inv.archived)
	for _, id := range inv.archived {
		if reason, err := admissionContext(ctx); err != nil {
			out.Reason = reason
			return inv, out, err
		}
		snap, _, _, loadErr := s.readArchiveRecord(id, true)
		if loadErr != nil {
			out.Reason, out.RecordID = admissionError(loadErr), id
			return inv, out, loadErr
		}
		if !archiveEligible(snap) {
			out.Reason, out.RecordID = AdmissionCorrupt, id
			return inv, out, ErrCorrupt
		}
	}
	if reason, err := admissionContext(ctx); err != nil {
		out.Reason = reason
		return inv, out, err
	}
	out.Reason = AdmissionNoBlocker
	out.Records, out.Bytes = len(inv.active), activeBytes
	out.ArchivedRecords, out.ArchivedBytes = len(inv.archived), archivedBytes
	out.InventoryComplete = true
	return inv, out, nil
}

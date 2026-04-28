package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lamht09/claude-account-switcher/internal/domain"
	"github.com/lamht09/claude-account-switcher/internal/lock"
	"github.com/lamht09/claude-account-switcher/internal/oauth"
	"github.com/lamht09/claude-account-switcher/internal/output"
	"github.com/lamht09/claude-account-switcher/internal/process"
	"github.com/lamht09/claude-account-switcher/internal/storage"
)

type Switcher struct {
	store *storage.Store
	lock  *lock.FileLock
}

func NewSwitcher() *Switcher {
	s := storage.NewStore()
	root, _ := s.Paths()
	return &Switcher{
		store: s,
		lock:  lock.New(root + "/.lock"),
	}
}

func (s *Switcher) setup() error {
	if err := s.store.EnsureDirs(); err != nil {
		return err
	}
	if err := s.store.InitSequenceIfMissing(); err != nil {
		return err
	}
	return s.lock.WithLock(func() error {
		seq, err := s.store.ReadSequence()
		if err != nil {
			return nil
		}
		if !s.ensureSequenceFingerprints(seq) {
			return nil
		}
		return s.store.WriteSequence(seq)
	})
}

func (s *Switcher) currentIdentity() (string, string, string, error) {
	cfg, _, err := s.store.ReadLiveConfig()
	if err != nil {
		return "", "", "", err
	}
	oauthAccount, ok := cfg["oauthAccount"].(map[string]any)
	if !ok {
		return "", "", "", domain.ErrConfigNotFound
	}
	email, _ := oauthAccount["emailAddress"].(string)
	if email == "" {
		return "", "", "", domain.ErrConfigNotFound
	}
	orgUUID, _ := oauthAccount["organizationUuid"].(string)
	accountUUID, _ := oauthAccount["accountUuid"].(string)
	if !domain.IsValidIdentity(accountUUID, orgUUID) {
		return "", "", "", fmt.Errorf("%w: live account is missing uuid", domain.ErrInvalidIdentity)
	}
	return email, orgUUID, accountUUID, nil
}

func (s *Switcher) readSequence() (*domain.SequenceData, error) {
	seq, err := s.store.ReadSequence()
	if err != nil {
		return nil, domain.ErrManagedNotFound
	}
	_ = s.ensureSequenceFingerprints(seq)
	return seq, nil
}

func (s *Switcher) Status() error {
	email, orgUUID, accountUUID, err := s.currentIdentity()
	if err != nil {
		output.Info("%s %s", output.Accent("Current profile:"), output.Muted("none detected (sign in via Claude Code first)"))
		return nil
	}
	profileLabel := output.Cyan(email)
	seq, err := s.readSequence()
	if err == nil {
		for num, account := range seq.Accounts {
			if domain.ManagedIdentityMatch(account, email, orgUUID, accountUUID) {
				profileLabel = output.Green("slot " + num)
				output.Info("%s %s %s", output.Accent("Current profile:"), profileLabel, output.Muted("("+email+")"))
				output.Info("%s %s", output.Accent("Managed slots:"), output.Bold(strconv.Itoa(len(seq.Accounts))))
				s.printStatusUsageAndOAuth(email, orgUUID, accountUUID)
				return nil
			}
		}
	}
	output.Info("%s %s %s", output.Accent("Current profile:"), profileLabel, output.Muted("(outside managed slots)"))
	s.printStatusUsageAndOAuth(email, orgUUID, accountUUID)
	return nil
}

func (s *Switcher) printStatusUsageAndOAuth(email, orgUUID, accountUUID string) {
	creds, credsErr := s.store.ReadLiveCredentials()
	usage := (*oauth.UsageResult)(nil)
	if credsErr == nil && creds != "" {
		usage, _, _ = fetchUsageForAccount(creds, true)
	}
	output.Info("%s", output.Accent("Usage:"))
	printUsageSummary("  ", usage)

	output.Info("%s %s", output.Accent("OAuth:"), output.Muted(fmt.Sprintf("email=%s org=%s account=%s", email, nonEmpty(orgUUID), nonEmpty(accountUUID))))
	tokenStatus := "oauth: unavailable"
	if credsErr == nil {
		if resolved := oauth.BuildTokenStatus(creds); resolved != "" {
			tokenStatus = resolved
		}
	}
	output.Info("  %s %s", output.Dim("•"), output.Dim(tokenStatus))
}

func printUsageSummary(prefix string, usage *oauth.UsageResult) {
	if usage == nil {
		output.Info("%s%s", prefix, output.Dim(output.Italic("usage stats unavailable")))
		return
	}
	usageLines := 0
	if usage.FiveHour != nil {
		usageLines++
	}
	if usage.SevenDay != nil {
		usageLines++
	}
	usageLineNo := 0
	if usage.FiveHour != nil {
		usageLineNo++
		connector := "└"
		if usageLineNo < usageLines {
			connector = "├"
		}
		if usage.FiveHour.Clock != "" {
			output.Info(
				"%s%s %s",
				prefix,
				output.Dim(connector),
				output.Dim(fmt.Sprintf("5h: %3.0f%%   resets %-12s  in %s", usage.FiveHour.Pct, usage.FiveHour.Clock, usage.FiveHour.Countdown)),
			)
		} else {
			output.Info("%s%s %s", prefix, output.Dim(connector), output.Dim(fmt.Sprintf("5h: %3.0f%%", usage.FiveHour.Pct)))
		}
	}
	if usage.SevenDay != nil {
		usageLineNo++
		connector := "└"
		if usageLineNo < usageLines {
			connector = "├"
		}
		if usage.SevenDay.Clock != "" {
			output.Info(
				"%s%s %s",
				prefix,
				output.Dim(connector),
				output.Dim(fmt.Sprintf("7d: %3.0f%%   resets %-12s  in %s", usage.SevenDay.Pct, usage.SevenDay.Clock, usage.SevenDay.Countdown)),
			)
		} else {
			output.Info("%s%s %s", prefix, output.Dim(connector), output.Dim(fmt.Sprintf("7d: %3.0f%%", usage.SevenDay.Pct)))
		}
	}
}

func (s *Switcher) Debug() error {
	root, seqPath := s.store.Paths()
	output.Info("%s", output.Bold("Debug Snapshot:"))
	output.Info("  backup root: %s", root)
	output.Info("  sequence path: %s", seqPath)

	currentEmail, currentOrg, currentUUID, currentErr := s.currentIdentity()
	if currentErr != nil {
		output.Info("  live identity: unavailable (%v)", currentErr)
	} else {
		output.Info("  live identity: email=%s org=%s uuid=%s", currentEmail, nonEmpty(currentOrg), nonEmpty(currentUUID))
	}

	liveCfg, liveCfgPath, liveCfgErr := s.store.ReadLiveConfig()
	if liveCfgErr != nil {
		output.Info("  live config: unreadable (%v)", liveCfgErr)
	} else {
		output.Info("  live config path: %s", liveCfgPath)
		oauthAccount, _ := liveCfg["oauthAccount"].(map[string]any)
		cfgEmail, _ := oauthAccount["emailAddress"].(string)
		cfgOrg, _ := oauthAccount["organizationUuid"].(string)
		cfgUUID, _ := oauthAccount["accountUuid"].(string)
		output.Info("  live oauthAccount: email=%s org=%s uuid=%s", nonEmpty(cfgEmail), nonEmpty(cfgOrg), nonEmpty(cfgUUID))
	}

	liveCreds, liveCredsErr := s.store.ReadLiveCredentials()
	if liveCredsErr != nil {
		output.Info("  live credentials: unreadable (%v)", liveCredsErr)
	} else {
		accessToken := oauth.ExtractAccessToken(liveCreds)
		tokenState := "missing accessToken"
		if accessToken != "" {
			tokenState = "accessToken present"
		}
		output.Info("  live credentials: %s", tokenState)
	}

	seq, seqErr := s.readSequence()
	if seqErr != nil {
		output.Info("  managed sequence: unavailable (%v)", seqErr)
		return nil
	}
	activeSlot := "(nil)"
	if seq.ActiveAccountNumber != nil {
		activeSlot = strconv.Itoa(*seq.ActiveAccountNumber)
	}
	output.Info("  active slot in sequence: %s", activeSlot)
	output.Info("  sequence order: %v", seq.Sequence)
	if len(seq.Sequence) == 0 {
		output.Info("  no managed slots")
		return nil
	}

	output.Info("")
	output.Info("%s", output.Bold("Managed Slots:"))
	for _, num := range seq.Sequence {
		key := strconv.Itoa(num)
		acc, ok := seq.Accounts[key]
		if !ok {
			output.Info("  slot %s: missing account metadata", key)
			continue
		}
		resolved := s.accountIdentityMatches(acc, currentEmail, currentOrg, currentUUID)
		output.Info(
			"  slot %s: email=%s org=%s uuid=%s matchLive=%t",
			key,
			acc.Email,
			nonEmpty(acc.OrganizationUUID),
			nonEmpty(acc.UUID),
			resolved,
		)

		backupCfg, backupCreds, backupErr := s.store.ReadAccountBackup(key, acc.Email)
		if backupErr != nil {
			output.Info("    backup: unreadable (%v)", backupErr)
			continue
		}
		backupEmail, backupOrg, backupUUID := extractBackupIdentity(backupCfg)
		backupToken := "missing accessToken"
		if oauth.ExtractAccessToken(backupCreds) != "" {
			backupToken = "accessToken present"
		}
		output.Info(
			"    backup oauthAccount: email=%s org=%s uuid=%s",
			nonEmpty(backupEmail),
			nonEmpty(backupOrg),
			nonEmpty(backupUUID),
		)
		output.Info("    backup credentials: %s", backupToken)
	}
	return nil
}

func (s *Switcher) List(showToken bool) error {
	seq, err := s.readSequence()
	if err != nil {
		output.Info("%s", output.Muted("No managed slots yet."))
		_ = s.firstRunSetup()
		return nil
	}
	if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
		return err
	}
	currentEmail, currentOrg, currentUUID, _ := s.currentIdentity()
	usageCache, cacheFresh := s.readUsageCache()
	expectedUsageKeys := map[string]struct{}{}
	type listRow struct {
		Num       int
		Key       string
		Account   domain.Account
		Active    bool
		BackupCfg string
		Creds     string
	}
	rows := make([]listRow, 0, len(seq.Sequence))
	for _, num := range seq.Sequence {
		key := strconv.Itoa(num)
		account, ok := seq.Accounts[key]
		if !ok {
			continue
		}
		active := s.accountIdentityMatches(account, currentEmail, currentOrg, currentUUID)
		backupCfg := ""
		creds := ""
		if active {
			if liveCreds, readErr := s.store.ReadLiveCredentials(); readErr == nil {
				creds = liveCreds
			}
		} else {
			backupCfg, creds, _ = s.store.ReadAccountBackup(key, account.Email)
		}
		if creds == "" {
			// Fallback keeps list/token-status useful when live credentials are unavailable.
			fallbackCfg, backupCreds, backupErr := s.store.ReadAccountBackup(key, account.Email)
			if backupErr == nil {
				if backupCfg == "" {
					backupCfg = fallbackCfg
				}
				creds = backupCreds
			}
		}
		rows = append(rows, listRow{
			Num:       num,
			Key:       key,
			Account:   account,
			Active:    active,
			BackupCfg: backupCfg,
			Creds:     creds,
		})
		expectedUsageKeys[s.usageCacheKey(account)] = struct{}{}
	}
	if cacheFresh {
		if len(usageCache) != len(expectedUsageKeys) {
			cacheFresh = false
		} else {
			for k := range expectedUsageKeys {
				if _, ok := usageCache[k]; !ok {
					cacheFresh = false
					break
				}
			}
		}
	}

	type usageFetchResult struct {
		Usage       *oauth.UsageResult
		UpdatedCred string
		Changed     bool
		PersistErr  error
	}
	fetched := map[string]usageFetchResult{}
	if !cacheFresh {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, row := range rows {
			row := row
			wg.Add(1)
			go func() {
				defer wg.Done()
				usage, updatedCreds, changed := fetchUsageForAccount(row.Creds, row.Active)
				result := usageFetchResult{
					Usage:       usage,
					UpdatedCred: updatedCreds,
					Changed:     changed && updatedCreds != "",
				}
				if result.Changed {
					result.PersistErr = s.lock.WithLock(func() error {
						if row.Active {
							return s.store.WriteLiveCredentials(updatedCreds)
						}
						return s.store.WriteAccountBackup(row.Key, row.Account.Email, row.BackupCfg, updatedCreds)
					})
				}
				mu.Lock()
				fetched[row.Key] = result
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	output.Info("%s", output.Bold(output.Cyan("Managed Accounts")))
	for idx, row := range rows {
		tag := row.Account.OrganizationName
		if tag == "" {
			tag = "personal"
		}
		tagText := output.Dim("[" + tag + "]")
		activeText := ""
		if row.Active {
			activeText = " " + output.Green(output.Bold("(active)"))
		}
		output.Info("  %d: %s %s%s", row.Num, row.Account.Email, tagText, activeText)

		creds := row.Creds
		var usage *oauth.UsageResult
		usageKey := s.usageCacheKey(row.Account)
		if cacheFresh {
			usage = usageCache[usageKey]
		} else if result, ok := fetched[row.Key]; ok {
			usage = result.Usage
			if result.Changed && result.PersistErr == nil {
				creds = result.UpdatedCred
			}
			if usage != nil {
				usageCache[usageKey] = usage
			}
		}
		printUsageSummary("     ", usage)

		if showToken {
			tokenStatus := oauth.BuildTokenStatus(creds)
			if tokenStatus != "" {
				output.Info("     %s %s", output.Dim("•"), output.Dim(tokenStatus))
			}
		}
		if idx < len(rows)-1 {
			output.Info("")
		}
	}
	for _, warning := range s.collectCredentialHealthWarnings(seq) {
		output.Info("%s %s", output.Yellow("Warning:"), warning)
	}
	if len(usageCache) > 0 {
		s.writeUsageCache(usageCache)
	}

	sessions := process.RunningSessions()
	ides := process.RunningIDEInstances()
	if len(sessions) > 0 || len(ides) > 0 {
		output.Info("")
		output.Info("%s", output.Bold(output.Cyan("Live Sessions")))
		type instanceGroup struct {
			Sessions int
			IDE      int
		}
		groups := map[string]*instanceGroup{}
		for _, session := range sessions {
			label := shortEntrypoint(session.Entrypoint)
			cwd := abbreviatePath(session.CWD)
			key := label + "|" + cwd
			if groups[key] == nil {
				groups[key] = &instanceGroup{}
			}
			groups[key].Sessions++
		}
		for _, ide := range ides {
			name := shortIDEName(ide.IDEName)
			if len(ide.WorkspaceFolders) == 0 {
				key := name + "|(unknown workspace)"
				if groups[key] == nil {
					groups[key] = &instanceGroup{}
				}
				groups[key].IDE++
				continue
			}
			for _, folder := range ide.WorkspaceFolders {
				key := name + "|" + abbreviatePath(folder)
				if groups[key] == nil {
					groups[key] = &instanceGroup{}
				}
				groups[key].IDE++
			}
		}
		keys := make([]string, 0, len(groups))
		for key := range groups {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts := strings.SplitN(key, "|", 2)
			label := parts[0]
			folder := parts[1]
			group := groups[key]
			types := make([]string, 0, 2)
			if group.Sessions > 0 {
				if group.Sessions == 1 {
					types = append(types, "1 terminal session")
				} else {
					types = append(types, fmt.Sprintf("%d terminal sessions", group.Sessions))
				}
			}
			if group.IDE > 0 {
				types = append(types, "IDE workspace")
			}
			output.Info(
				"  %s %s   %s %s",
				output.Dim("●"),
				output.Cyan(label),
				output.Dim(folder),
				output.Dim("("+strings.Join(types, ", ")+")"),
			)
		}
	}
	return nil
}

func (s *Switcher) Add(slot int) error {
	if err := s.setup(); err != nil {
		return err
	}
	return s.lock.WithLock(func() error {
		email, orgUUID, accountUUID, err := s.currentIdentity()
		if err != nil {
			return err
		}
		seq, _ := s.readSequence()
		if seq == nil {
			seq = &domain.SequenceData{Accounts: map[string]domain.Account{}, SlotFingerprints: map[string]string{}}
		}
		s.ensureSequenceFingerprints(seq)
		if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
			return err
		}
		if err := s.failOnCriticalHealth(seq); err != nil {
			return err
		}
		existing := s.accountNumberByIdentity(seq, email, orgUUID, accountUUID)

		// Source-aligned behavior: when slot is not specified and identity already
		// exists, refresh backup in-place instead of rewriting slot metadata.
		if slot <= 0 && existing != "" {
			cfgRaw, creds, readErr := s.readLiveRaw()
			if readErr != nil {
				return readErr
			}
			if err := s.validateDistinctAccessToken(seq, existing, creds); err != nil {
				return err
			}
			existingAcc := seq.Accounts[existing]
			existingAcc.Fingerprint = domain.AccountFingerprint(accountUUID, orgUUID, email)
			seq.Accounts[existing] = existingAcc
			seq.SlotFingerprints[existing] = existingAcc.Fingerprint
			if err := s.store.WriteAccountBackup(existing, existingAcc.Email, cfgRaw, creds); err != nil {
				return err
			}
			existingNum, _ := strconv.Atoi(existing)
			seq.ActiveAccountNumber = &existingNum
			if err := s.store.WriteSequence(seq); err != nil {
				return err
			}
			output.Info("%s slot %d %s", output.Green("Refreshed credentials for"), existingNum, output.Muted("("+existingAcc.Email+")"))
			return nil
		}

		target := slot
		if target <= 0 {
			target = s.store.NextAccountNumber(seq)
		} else if target < 1 {
			return errors.New("slot number must be >= 1")
		}
		targetKey := strconv.Itoa(target)

		var displaceSlot *struct {
			Number int
			Email  string
		}
		var migrateFrom *struct {
			Number int
			Email  string
		}

		// Same account explicitly moved to another slot.
		if slot > 0 && existing != "" && existing != targetKey {
			oldAcc := seq.Accounts[existing]
			oldNum, _ := strconv.Atoi(existing)
			migrateFrom = &struct {
				Number int
				Email  string
			}{
				Number: oldNum,
				Email:  oldAcc.Email,
			}
		}

		// Target occupied by a different identity.
		if targetAcc, ok := seq.Accounts[targetKey]; ok && !s.accountIdentityMatches(targetAcc, email, orgUUID, accountUUID) {
			okToOverwrite, confirmErr := confirmPrompt(fmt.Sprintf(
				"Slot %d currently belongs to %s. Replace it? [y/N] ",
				target,
				targetAcc.Email,
			))
			if confirmErr != nil {
				return confirmErr
			}
			if !okToOverwrite {
				output.Info("%s", output.Muted("Action cancelled."))
				return nil
			}
			displaceSlot = &struct {
				Number int
				Email  string
			}{
				Number: target,
				Email:  targetAcc.Email,
			}
		}

		// Read live data before any destructive cleanup.
		cfgRaw, creds, err := s.readLiveRaw()
		if err != nil {
			return err
		}

		removeSlot := func(number int, backupEmail string) {
			key := strconv.Itoa(number)
			acc, ok := seq.Accounts[key]
			keepCredential := s.isFingerprintShared(seq, key)
			if ok {
				s.store.DeleteAccountBackup(key, acc.Email, keepCredential)
			}
			if backupEmail != "" && (!ok || acc.Email != backupEmail) {
				s.store.DeleteAccountBackup(key, backupEmail, keepCredential)
			}
			delete(seq.Accounts, key)
			delete(seq.SlotFingerprints, key)
			seq.Sequence = filterOut(seq.Sequence, number)
		}

		if displaceSlot != nil {
			removeSlot(displaceSlot.Number, displaceSlot.Email)
		}
		if migrateFrom != nil && migrateFrom.Number != target {
			removeSlot(migrateFrom.Number, migrateFrom.Email)
			output.Info("%s %d %s %d", output.Accent("Relocated account from slot"), migrateFrom.Number, output.Accent("to slot"), target)
		}

		if accountUUID == "" {
			accountUUID = extractAccountUUID(cfgRaw)
		}
		addedAt := domain.TimestampNow()
		if prev, ok := seq.Accounts[targetKey]; ok && prev.Added != "" {
			addedAt = prev.Added
		}
		seq.Accounts[targetKey] = domain.Account{
			Email:            email,
			UUID:             accountUUID,
			OrganizationUUID: orgUUID,
			OrganizationName: extractOrgName(cfgRaw),
			Fingerprint:      domain.AccountFingerprint(accountUUID, orgUUID, email),
			Added:            addedAt,
		}
		if seq.SlotFingerprints == nil {
			seq.SlotFingerprints = map[string]string{}
		}
		seq.SlotFingerprints[targetKey] = seq.Accounts[targetKey].Fingerprint
		if !contains(seq.Sequence, target) {
			seq.Sequence = append(seq.Sequence, target)
		}
		sort.Ints(seq.Sequence)
		seq.ActiveAccountNumber = &target
		if err := s.validateDistinctAccessToken(seq, targetKey, creds); err != nil {
			return err
		}
		if err := s.store.WriteAccountBackup(targetKey, email, cfgRaw, creds); err != nil {
			return err
		}
		if err := s.store.WriteSequence(seq); err != nil {
			return err
		}
		if err := s.verifySlotPersistence(targetKey, seq.Accounts[targetKey]); err != nil {
			return err
		}
		output.Info("%s %s %s", output.Green("Saved"), output.Bold(fmt.Sprintf("slot %d", target)), output.Muted("for "+email))
		return nil
	})
}

func (s *Switcher) Remove(identifier string) error {
	return s.lock.WithLock(func() error {
		seq, err := s.readSequence()
		if err != nil {
			return err
		}
		if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
			return err
		}
		num, err := s.resolveManagedIdentifier(seq, identifier, "remove")
		if err != nil {
			if errors.Is(err, errSelectionCancelled) {
				return nil
			}
			return err
		}
		acc := seq.Accounts[num]
		if seq.ActiveAccountNumber != nil && strconv.Itoa(*seq.ActiveAccountNumber) == num {
			output.Info("%s Account-%s %s", output.Yellow("Warning:"), num, output.Muted("("+acc.Email+") is active now"))
		}
		okToRemove, confirmErr := confirmPrompt(
			fmt.Sprintf("Permanently delete Account-%s (%s)? [y/N] ", num, acc.Email),
		)
		if confirmErr != nil {
			return confirmErr
		}
		if !okToRemove {
			output.Info("%s", output.Muted("Action cancelled."))
			return nil
		}
		keepCredential := s.isFingerprintShared(seq, num)
		s.store.DeleteAccountBackup(num, acc.Email, keepCredential)
		delete(seq.Accounts, num)
		delete(seq.SlotFingerprints, num)
		n, _ := strconv.Atoi(num)
		seq.Sequence = filterOut(seq.Sequence, n)
		output.Info("%s %s %s", output.Green("Removed"), output.Bold("slot "+num), output.Muted("("+acc.Email+")"))
		return s.store.WriteSequence(seq)
	})
}

func (s *Switcher) Switch() error {
	seq, err := s.readSequence()
	if err != nil {
		return err
	}
	if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
		return err
	}

	email, orgUUID, accountUUID, err := s.currentIdentity()
	if err == nil && s.accountNumberByIdentity(seq, email, orgUUID, accountUUID) == "" {
		output.Info("%s active profile %q is not managed yet.", output.Accent("Notice:"), email)
		if err := s.Add(0); err != nil {
			return err
		}
		if refreshed, readErr := s.readSequence(); readErr == nil && refreshed.ActiveAccountNumber != nil {
			output.Info("It was auto-saved as %s.", output.Bold(fmt.Sprintf("Account-%d", *refreshed.ActiveAccountNumber)))
		}
		output.Info("%s", output.Muted("Run `ca switch` again to rotate to the next slot."))
		return nil
	}

	err = s.lock.WithLock(func() error {
		latest, err := s.readSequence()
		if err != nil {
			return err
		}
		if len(latest.Sequence) < 2 {
			output.Info("%s", output.Muted("Only one managed slot exists. Add another to rotate accounts."))
			return nil
		}
		current := 0
		currentNumStr, _, _ := s.resolveCurrentSlotStrict(latest)
		if currentNumStr != "" {
			if n, parseErr := strconv.Atoi(currentNumStr); parseErr == nil {
				current = n
			}
		}
		if current == 0 {
			return fmt.Errorf("%w: could not resolve current slot by uuid identity", domain.ErrInvalidIdentity)
		}
		idx := 0
		for i, n := range latest.Sequence {
			if n == current {
				idx = i
				break
			}
		}
		next := latest.Sequence[(idx+1)%len(latest.Sequence)]
		return s.switchToNumberLocked(next, latest)
	})
	if err != nil {
		return err
	}
	return s.showPostSwitchDetails()
}

func (s *Switcher) SwitchTo(identifier string) error {
	seq, err := s.readSequence()
	if err != nil {
		return err
	}
	if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
		return err
	}
	num, err := s.resolveManagedIdentifier(seq, identifier, "switch")
	if err != nil {
		if errors.Is(err, errSelectionCancelled) {
			return nil
		}
		return err
	}
	n, _ := strconv.Atoi(num)
	err = s.lock.WithLock(func() error {
		latest, err := s.readSequence()
		if err != nil {
			return err
		}
		return s.switchToNumberLocked(n, latest)
	})
	if err != nil {
		return err
	}
	return s.showPostSwitchDetails()
}

func (s *Switcher) switchToNumberLocked(target int, seq *domain.SequenceData) error {
	targetKey := strconv.Itoa(target)
	targetAcc, ok := seq.Accounts[targetKey]
	if !ok {
		return domain.ErrAccountNotFound
	}
	currentCfgRaw, currentCreds, err := s.readLiveRaw()
	if err != nil {
		return err
	}
	if _, _, _, identityErr := s.currentIdentity(); identityErr != nil {
		return errors.New("cannot switch: no active account in Claude Code")
	}

	currentKey, resolveStatus, _ := s.resolveCurrentSlotStrict(seq)
	currentKeyResolvedByIdentity := resolveStatus == "resolved_by_identity"
	if currentKey == "" {
		return fmt.Errorf("%w: cannot resolve current managed slot by uuid identity", domain.ErrInvalidIdentity)
	}
	currentBackupEmail := ""
	if currentKey != "" {
		if currentAcc, exists := seq.Accounts[currentKey]; exists && currentAcc.Email != "" {
			currentBackupEmail = currentAcc.Email
		}
	}

	backupCfg, backupCreds, err := s.store.ReadAccountBackup(targetKey, targetAcc.Email)
	if err != nil {
		return err
	}

	var originalCfg map[string]any
	if err := json.Unmarshal([]byte(currentCfgRaw), &originalCfg); err != nil {
		return err
	}
	txn := newSwitchTxn(s, seq, currentCreds, originalCfg, seq.ActiveAccountNumber)

	if currentKey != "" && currentBackupEmail != "" && currentKeyResolvedByIdentity {
		_ = s.store.WriteAccountBackup(currentKey, currentBackupEmail, currentCfgRaw, currentCreds)
	}
	if err := s.store.WriteLiveCredentials(backupCreds); err != nil {
		return err
	}
	txn.Record(switchStepCredentialsWritten)
	targetCfg := map[string]any{}
	if err := json.Unmarshal([]byte(backupCfg), &targetCfg); err != nil {
		return txn.FailWithRollback(err)
	}
	targetOAuth, ok := targetCfg["oauthAccount"]
	if !ok {
		return txn.FailWithRollback(errors.New("invalid backup config: missing oauthAccount"))
	}
	liveCfg, liveCfgPath, err := s.store.ReadLiveConfig()
	if err != nil {
		return txn.FailWithRollback(err)
	}
	liveCfg["oauthAccount"] = targetOAuth
	if err := s.store.WriteLiveConfig(liveCfgPath, liveCfg); err != nil {
		return txn.FailWithRollback(err)
	}
	txn.Record(switchStepConfigWritten)
	seq.ActiveAccountNumber = &target
	if err := s.store.WriteSequence(seq); err != nil {
		return txn.FailWithRollback(err)
	}
	txn.Record(switchStepSequenceUpdated)
	if err := s.verifySlotPersistence(targetKey, targetAcc); err != nil {
		return txn.FailWithRollback(err)
	}
	output.Info("%s %s %s", output.Green("Now using"), output.Bold(fmt.Sprintf("Account-%d", target)), output.Muted("("+targetAcc.Email+")"))
	return nil
}

const (
	switchStepCredentialsWritten = "credentials_written"
	switchStepConfigWritten      = "config_written"
	switchStepSequenceUpdated    = "sequence_updated"
)

type switchTxn struct {
	switcher       *Switcher
	seq            *domain.SequenceData
	originalCreds  string
	originalCfg    map[string]any
	originalActive *int
	completedSteps []string
}

func newSwitchTxn(s *Switcher, seq *domain.SequenceData, originalCreds string, originalCfg map[string]any, originalActive *int) *switchTxn {
	return &switchTxn{
		switcher:       s,
		seq:            seq,
		originalCreds:  originalCreds,
		originalCfg:    originalCfg,
		originalActive: originalActive,
		completedSteps: make([]string, 0, 3),
	}
}

func (t *switchTxn) Record(step string) {
	t.completedSteps = append(t.completedSteps, step)
}

func (t *switchTxn) FailWithRollback(cause error) error {
	if rollbackErr := t.Rollback(); rollbackErr != nil {
		return fmt.Errorf("switch failed and rollback failed: %w (rollback error: %v)", cause, rollbackErr)
	}
	return cause
}

func (t *switchTxn) Rollback() error {
	for i := len(t.completedSteps) - 1; i >= 0; i-- {
		switch t.completedSteps[i] {
		case switchStepSequenceUpdated:
			t.seq.ActiveAccountNumber = t.originalActive
			if writeErr := t.switcher.store.WriteSequence(t.seq); writeErr != nil {
				return writeErr
			}
		case switchStepConfigWritten:
			_, liveCfgPath, readErr := t.switcher.store.ReadLiveConfig()
			if readErr != nil {
				return readErr
			}
			if writeErr := t.switcher.store.WriteLiveConfig(liveCfgPath, t.originalCfg); writeErr != nil {
				return writeErr
			}
		case switchStepCredentialsWritten:
			if writeErr := t.switcher.store.WriteLiveCredentials(t.originalCreds); writeErr != nil {
				return writeErr
			}
		}
	}
	return nil
}

func (s *Switcher) showPostSwitchDetails() error {
	if err := s.List(false); err != nil {
		output.Info("  %s", output.Muted("(usage panel unavailable, rerun `ca list`)"))
	}
	output.Info("")
	output.Info("%s", output.Yellow("Restart Claude Code so the new authentication takes effect."))
	return nil
}

func (s *Switcher) Purge() error {
	root, _ := s.store.Paths()
	output.Info("%s", output.Yellow("This action removes all claude-swap data from this machine:"))
	output.Info("  - Backup directory: %s", root)
	output.Info("")
	output.Info("%s", output.Dim("Note: your current Claude Code login session is kept intact."))
	output.Info("")
	okToPurge, err := confirmPrompt(
		"Confirm full data purge? [y/N] ",
	)
	if err != nil {
		return err
	}
	if !okToPurge {
		output.Info("%s", output.Muted("Action cancelled."))
		return nil
	}
	return s.store.Purge()
}

func (s *Switcher) readLiveRaw() (cfgRaw, creds string, err error) {
	cfgMap, path, err := s.store.ReadLiveConfig()
	if err != nil {
		return "", "", err
	}
	cfgBytes, _ := json.MarshalIndent(cfgMap, "", "  ")
	_ = path
	creds, err = s.store.ReadLiveCredentials()
	if err != nil {
		return "", "", err
	}
	return string(cfgBytes), creds, nil
}

func extractOrgName(cfgRaw string) string {
	var cfg map[string]any
	if json.Unmarshal([]byte(cfgRaw), &cfg) != nil {
		return ""
	}
	oauthAccount, ok := cfg["oauthAccount"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := oauthAccount["organizationName"].(string)
	return name
}

func extractAccountUUID(cfgRaw string) string {
	var cfg map[string]any
	if json.Unmarshal([]byte(cfgRaw), &cfg) != nil {
		return ""
	}
	oauthAccount, ok := cfg["oauthAccount"].(map[string]any)
	if !ok {
		return ""
	}
	uuid, _ := oauthAccount["accountUuid"].(string)
	return uuid
}

func extractBackupIdentity(cfgRaw string) (email, orgUUID, accountUUID string) {
	var cfg map[string]any
	if json.Unmarshal([]byte(cfgRaw), &cfg) != nil {
		return "", "", ""
	}
	oauthAccount, ok := cfg["oauthAccount"].(map[string]any)
	if !ok {
		return "", "", ""
	}
	email, _ = oauthAccount["emailAddress"].(string)
	orgUUID, _ = oauthAccount["organizationUuid"].(string)
	accountUUID, _ = oauthAccount["accountUuid"].(string)
	return email, orgUUID, accountUUID
}

func nonEmpty(v string) string {
	if v == "" {
		return "(empty)"
	}
	return v
}

func contains(arr []int, v int) bool {
	for _, n := range arr {
		if n == v {
			return true
		}
	}
	return false
}

func filterOut(arr []int, v int) []int {
	out := make([]int, 0, len(arr))
	for _, n := range arr {
		if n != v {
			out = append(out, n)
		}
	}
	return out
}

func ToExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, domain.ErrAmbiguousIdentity) {
		fmt.Println(output.Yellow("Ambiguous selection detected. Please specify the slot number."))
		return 2
	}
	fmt.Printf("%s %v\n", output.Red("Command failed:"), err)
	return 1
}

var errNoTTYConfirmation = errors.New("interactive confirmation required but no TTY available")
var errSelectionCancelled = errors.New("interactive selection cancelled")
var confirmPrompt = promptConfirm
var confirmPromptDefaultYes = promptConfirmDefaultYes
var stdinIsTTY = defaultIsTTY
var fetchUsageForAccount = oauth.FetchUsageForAccount

func (s *Switcher) accountNumberByIdentity(seq *domain.SequenceData, email, orgUUID, accountUUID string) string {
	if strings.TrimSpace(accountUUID) == "" {
		return ""
	}
	matches := make([]string, 0, len(seq.Accounts))
	for num, account := range seq.Accounts {
		if domain.ManagedIdentityMatch(account, email, orgUUID, accountUUID) {
			matches = append(matches, num)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func (s *Switcher) accountIdentityMatches(account domain.Account, email, orgUUID, accountUUID string) bool {
	return domain.ManagedIdentityMatch(account, email, orgUUID, accountUUID)
}

func (s *Switcher) resolveManagedIdentifier(seq *domain.SequenceData, identifier, action string) (string, error) {
	if _, err := strconv.Atoi(identifier); err == nil {
		if _, ok := seq.Accounts[identifier]; ok {
			return identifier, nil
		}
		return "", domain.ErrAccountNotFound
	}
	matches := make([]string, 0)
	for num, account := range seq.Accounts {
		if account.Email == identifier {
			matches = append(matches, num)
		}
	}
	if len(matches) == 0 {
		return "", domain.ErrAccountNotFound
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if !isTTY() {
		return "", fmt.Errorf("%w: interactive selection required", domain.ErrAmbiguousIdentity)
	}
	sort.Slice(matches, func(i, j int) bool {
		li, _ := strconv.Atoi(matches[i])
		lj, _ := strconv.Atoi(matches[j])
		return li < lj
	})
	output.Info("%s %q:", output.Accent("Multiple slots match"), identifier)
	for _, match := range matches {
		acc := seq.Accounts[match]
		tag := acc.OrganizationName
		if tag == "" {
			tag = "personal"
		}
		output.Info("  %s: %s [%s]", match, acc.Email, tag)
	}
	reader := bufio.NewReader(os.Stdin)
	output.Info("%s %s:", output.Accent("Pick a slot number to"), action)
	choiceRaw, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			output.Info("%s", output.Muted("Selection cancelled."))
			return "", errSelectionCancelled
		}
		return "", err
	}
	choice := strings.TrimSpace(choiceRaw)
	for _, match := range matches {
		if choice == match {
			return choice, nil
		}
	}
	output.Info("%s", output.Muted("Selection cancelled."))
	return "", errSelectionCancelled
}

func promptConfirm(message string) (bool, error) {
	if !isTTY() {
		return false, errNoTTYConfirmation
	}
	fmt.Print(message)
	reader := bufio.NewReader(os.Stdin)
	resp, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, errNoTTYConfirmation
		}
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(resp))
	return answer == "y" || answer == "yes", nil
}

func isTTY() bool {
	return stdinIsTTY()
}

func defaultIsTTY() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func shortEntrypoint(entrypoint string) string {
	switch entrypoint {
	case "cli":
		return "CLI"
	case "claude-vscode":
		return "VS Code"
	case "claude-desktop":
		return "Desktop"
	case "sdk-cli", "sdk-ts", "sdk-py":
		return "SDK"
	case "mcp":
		return "MCP"
	case "local-agent":
		return "Agent"
	case "remote":
		return "Remote"
	default:
		if entrypoint == "" {
			return "Session"
		}
		return entrypoint
	}
}

func shortIDEName(name string) string {
	if name == "Visual Studio Code" {
		return "VS Code"
	}
	if name == "" {
		return "IDE"
	}
	return name
}

func abbreviatePath(path string) string {
	if path == "" {
		return "(unknown)"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

type usageCacheFile struct {
	Timestamp int64                         `json:"timestamp"`
	Data      map[string]*oauth.UsageResult `json:"data"`
}

const usageCacheTTL = 15 * time.Second

func (s *Switcher) readUsageCache() (map[string]*oauth.UsageResult, bool) {
	root, _ := s.store.Paths()
	path := filepath.Join(root, "cache", "usage.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]*oauth.UsageResult{}, false
	}
	var cache usageCacheFile
	if err := json.Unmarshal(raw, &cache); err != nil {
		return map[string]*oauth.UsageResult{}, false
	}
	if time.Now().Unix()-cache.Timestamp > int64(usageCacheTTL/time.Second) {
		return map[string]*oauth.UsageResult{}, false
	}
	if cache.Data == nil {
		cache.Data = map[string]*oauth.UsageResult{}
	}
	return cache.Data, true
}

func (s *Switcher) writeUsageCache(data map[string]*oauth.UsageResult) {
	root, _ := s.store.Paths()
	path := filepath.Join(root, "cache", "usage.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	body, _ := json.Marshal(usageCacheFile{
		Timestamp: time.Now().Unix(),
		Data:      data,
	})
	_ = os.WriteFile(path, body, 0o600)
}

func (s *Switcher) usageCacheKey(account domain.Account) string {
	if account.Fingerprint != "" {
		return account.Fingerprint
	}
	if fp := domain.AccountFingerprint(account.UUID, account.OrganizationUUID, account.Email); fp != "" {
		return fp
	}
	return "added:" + account.Added
}

func (s *Switcher) validateDistinctAccessToken(seq *domain.SequenceData, targetKey, targetCreds string) error {
	targetToken := oauth.ExtractAccessToken(targetCreds)
	if targetToken == "" {
		return nil
	}
	for key, account := range seq.Accounts {
		if key == targetKey {
			continue
		}
		_, creds, err := s.store.ReadAccountBackup(key, account.Email)
		if err != nil || creds == "" {
			continue
		}
		if oauth.ExtractAccessToken(creds) == targetToken {
			return fmt.Errorf(
				"refusing to save slot %s: credentials match slot %s (%s); sign in to the intended account first",
				targetKey,
				key,
				account.Email,
			)
		}
	}
	return nil
}

func (s *Switcher) ensureSequenceFingerprints(seq *domain.SequenceData) bool {
	changed := false
	if seq.Accounts == nil {
		seq.Accounts = map[string]domain.Account{}
		changed = true
	}
	if seq.SlotFingerprints == nil {
		seq.SlotFingerprints = map[string]string{}
		changed = true
	}
	for slotKey, account := range seq.Accounts {
		// Always reconcile from uuid + organizationUuid in metadata. Older rows could
		// carry a non-empty fingerprint that disagrees with organizationUuid (e.g. wrong
		// org segment); repair and readSequence rely on this staying canonical.
		canonical := domain.AccountFingerprint(account.UUID, account.OrganizationUUID, account.Email)
		fp := canonical
		if fp == "" && strings.TrimSpace(account.Fingerprint) != "" {
			fp = strings.TrimSpace(account.Fingerprint)
		}
		if fp == "" {
			continue
		}
		if account.Fingerprint != fp {
			account.Fingerprint = fp
			seq.Accounts[slotKey] = account
			changed = true
		}
		if seq.SlotFingerprints[slotKey] != fp {
			seq.SlotFingerprints[slotKey] = fp
			changed = true
		}
	}
	for slotKey := range seq.SlotFingerprints {
		if _, ok := seq.Accounts[slotKey]; !ok {
			delete(seq.SlotFingerprints, slotKey)
			changed = true
		}
	}
	return changed
}

func (s *Switcher) collectCredentialHealthWarnings(seq *domain.SequenceData) []string {
	warnings := []string{}
	seenToken := map[string]string{}
	for slotKey, account := range seq.Accounts {
		if !domain.IsValidIdentity(account.UUID, account.OrganizationUUID) {
			warnings = append(warnings, fmt.Sprintf("slot %s (%s) has invalid identity metadata", slotKey, account.Email))
			continue
		}
		if seq.SlotFingerprints[slotKey] == "" {
			warnings = append(warnings, fmt.Sprintf("slot %s missing fingerprint mapping", slotKey))
		}
		_, creds, err := s.store.ReadAccountBackup(slotKey, account.Email)
		if err != nil || creds == "" {
			warnings = append(warnings, fmt.Sprintf("slot %s (%s) missing backup credentials", slotKey, account.Email))
			continue
		}
		token := oauth.ExtractAccessToken(creds)
		if token == "" {
			continue
		}
		if other, exists := seenToken[token]; exists && other != slotKey {
			warnings = append(warnings, fmt.Sprintf("slots %s and %s share the same access token", other, slotKey))
		} else {
			seenToken[token] = slotKey
		}
	}
	return warnings
}

func (s *Switcher) failOnCriticalHealth(seq *domain.SequenceData) error {
	for _, warning := range s.collectCredentialHealthWarnings(seq) {
		if strings.Contains(warning, "share the same access token") || strings.Contains(warning, "invalid identity metadata") {
			return fmt.Errorf("credential drift detected: %s; run `ca repair`", warning)
		}
	}
	return nil
}

func (s *Switcher) resolveCurrentSlotStrict(seq *domain.SequenceData) (string, string, error) {
	email, orgUUID, accountUUID, err := s.currentIdentity()
	if err != nil {
		return "", "no_live_identity", err
	}
	if current := s.accountNumberByIdentity(seq, email, orgUUID, accountUUID); current != "" {
		return current, "resolved_by_identity", nil
	}
	return "", "unmanaged_live_identity", nil
}

func (s *Switcher) verifySlotPersistence(slotKey string, account domain.Account) error {
	seq, err := s.readSequence()
	if err != nil {
		return err
	}
	got, ok := seq.Accounts[slotKey]
	if !ok {
		return fmt.Errorf("post-write verification failed: slot %s not found", slotKey)
	}
	if got.Fingerprint == "" || got.Fingerprint != account.Fingerprint {
		return fmt.Errorf("post-write verification failed: slot %s fingerprint mismatch", slotKey)
	}
	if seq.SlotFingerprints[slotKey] != account.Fingerprint {
		return fmt.Errorf("post-write verification failed: slot %s mapping mismatch", slotKey)
	}
	return nil
}

func (s *Switcher) Repair() error {
	if err := s.setup(); err != nil {
		return err
	}
	return s.lock.WithLock(func() error {
		seq, err := s.readSequence()
		if err != nil {
			return err
		}
		if err := s.ensureNoInvalidManagedAccounts(seq); err != nil {
			return err
		}
		changed := s.ensureSequenceFingerprints(seq)
		if changed {
			output.Info("%s", output.Green("Repaired sequence fingerprints"), output.Muted("(aligned to account uuid + organizationUuid in metadata)"))
		}
		warnings := s.collectCredentialHealthWarnings(seq)
		if len(warnings) == 0 {
			output.Info("%s", output.Green("No credential drift found."))
		} else {
			output.Info("%s", output.Bold(output.Cyan("Credential Health Report")))
			for _, warning := range warnings {
				output.Info("  - %s", warning)
			}
		}
		currentKey, status, _ := s.resolveCurrentSlotStrict(seq)
		if status == "resolved_by_identity" && currentKey != "" {
			acc := seq.Accounts[currentKey]
			cfgRaw, creds, readErr := s.readLiveRaw()
			if readErr == nil && creds != "" {
				if writeErr := s.store.WriteAccountBackup(currentKey, acc.Email, cfgRaw, creds); writeErr == nil {
					output.Info("%s %s %s", output.Green("Rebuilt backup credentials for"), output.Bold("slot "+currentKey), output.Muted("("+acc.Email+")"))
				}
			}
		}
		if changed {
			return s.store.WriteSequence(seq)
		}
		return nil
	})
}

func (s *Switcher) ensureNoInvalidManagedAccounts(seq *domain.SequenceData) error {
	invalid := make([]string, 0)
	for slot, account := range seq.Accounts {
		if !domain.IsValidIdentity(account.UUID, account.OrganizationUUID) {
			invalid = append(invalid, fmt.Sprintf("slot %s (%s)", slot, account.Email))
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: managed accounts missing uuid (%s). Re-login and re-add each listed account",
		domain.ErrInvalidIdentity,
		strings.Join(invalid, ", "),
	)
}

func (s *Switcher) isFingerprintShared(seq *domain.SequenceData, slotKey string) bool {
	target := seq.SlotFingerprints[slotKey]
	if target == "" {
		return false
	}
	for key, fp := range seq.SlotFingerprints {
		if key == slotKey {
			continue
		}
		if fp == target {
			return true
		}
	}
	return false
}

func (s *Switcher) firstRunSetup() error {
	email, _, _, err := s.currentIdentity()
	if err != nil {
		output.Info("%s", output.Muted("No active Claude account found. Sign in via Claude Code first."))
		return nil
	}
	if !isTTY() {
		output.Info("%s", output.Muted("No managed slots found. Run `ca add` after signing in."))
		return nil
	}
	ok, confirmErr := confirmPromptDefaultYes(fmt.Sprintf(
		"No slots are managed yet. Add current account (%s)? [Y/n] ",
		email,
	))
	if confirmErr != nil {
		return confirmErr
	}
	if !ok {
		output.Info("%s", output.Muted("Setup cancelled. You can run `ca add` later."))
		return nil
	}
	return s.Add(0)
}

func promptConfirmDefaultYes(message string) (bool, error) {
	if !isTTY() {
		return false, errNoTTYConfirmation
	}
	fmt.Print(message)
	reader := bufio.NewReader(os.Stdin)
	resp, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return false, errNoTTYConfirmation
		}
		return false, err
	}
	answer := strings.TrimSpace(strings.ToLower(resp))
	if answer == "" {
		return true, nil
	}
	return answer == "y" || answer == "yes", nil
}

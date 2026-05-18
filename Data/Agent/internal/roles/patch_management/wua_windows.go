//go:build windows

package patchmanagement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"
)

type wuaLogger func(string, ...any)

type wuaCOMAdapter struct {
	logger wuaLogger
}

func defaultWUAAdapter() WUAAdapter {
	return wuaCOMAdapter{}
}

func defaultWUAAdapterWithLogger(logf func(string, ...any)) WUAAdapter {
	return wuaCOMAdapter{logger: logf}
}

func (w wuaCOMAdapter) log(format string, args ...any) {
	if w.logger != nil {
		w.logger(format, args...)
	}
}

func (w wuaCOMAdapter) Scan(ctx context.Context) ([]Update, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	session, cleanup, err := newUpdateSession()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	updates, cleanupUpdates, err := searchUpdateCollection(session)
	if err != nil {
		return nil, err
	}
	defer cleanupUpdates()
	return updatesFromCollection(updates)
}

func (w wuaCOMAdapter) Install(ctx context.Context, updates []Update) (InstallSummary, error) {
	startedAt := time.Now().Unix()
	summary := InstallSummary{StartedAt: startedAt, ResultCode: "not_started"}
	if len(updates) == 0 {
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "no_updates"
		return summary, nil
	}
	w.log("install wua_session_start requested_updates=%d", len(updates))
	session, cleanup, err := newUpdateSession()
	if err != nil {
		w.log("install wua_session_failed error=%v", err)
		return summary, err
	}
	defer cleanup()
	selected := map[string]Update{}
	for _, update := range updates {
		selected[updateKey(update)] = update
	}
	w.log("install wua_live_scan_start")
	liveUpdates, cleanupLiveUpdates, err := searchUpdateCollection(session)
	if err != nil {
		w.log("install wua_live_scan_failed error=%v", err)
		return summary, err
	}
	defer cleanupLiveUpdates()
	w.log("install wua_live_scan_complete updates=%d", int(getIntProperty(liveUpdates, "Count")))
	collection, collectionCleanup, candidates, err := buildSelectedUpdateCollection(liveUpdates, selected)
	if err != nil {
		w.log("install wua_collection_failed error=%v", err)
		return summary, err
	}
	defer collectionCleanup()
	w.log("install wua_collection_ready candidates=%d collection_count=%d", len(candidates), int(getIntProperty(collection, "Count")))
	if len(candidates) == 0 {
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "no_matching_updates"
		w.log("install wua_no_matching_updates requested_updates=%d", len(updates))
		return summary, nil
	}
	for _, candidate := range candidates {
		w.log("install wua_candidate update_id=%s revision=%d downloaded=%t kbs=%s title=%q", candidate.UpdateID, candidate.Revision, candidate.IsDownloaded, strings.Join(candidate.KBArticleIDs, ","), candidate.Title)
	}
	w.log("install wua_batch_start candidates=%d", len(candidates))
	downloaded, err := downloadAndInstall(ctx, session, collection, candidates, false, w.log)
	if err == nil && !strings.EqualFold(downloaded.ResultCode, "failed") {
		w.log("install wua_batch_complete result=%s hresult=%s reboot_required=%t results=%d", downloaded.ResultCode, downloaded.HResult, downloaded.RebootRequired, len(downloaded.Results))
		return downloaded, nil
	}
	w.log("install wua_batch_failed result=%s hresult=%s error=%v", downloaded.ResultCode, downloaded.HResult, err)
	individual := InstallSummary{
		StartedAt:  startedAt,
		FinishedAt: time.Now().Unix(),
		ResultCode: "completed_with_individual_retry",
		HResult:    downloaded.HResult,
		Results:    []Update{},
	}
	var firstErr error = err
	for _, update := range candidates {
		w.log("install wua_individual_start update_id=%s revision=%d kbs=%s", update.UpdateID, update.Revision, strings.Join(update.KBArticleIDs, ","))
		singleCollection, singleCleanup, singleRows, buildErr := buildSelectedUpdateCollection(liveUpdates, map[string]Update{updateKey(update): update})
		if buildErr != nil {
			w.log("install wua_individual_collection_failed update_id=%s revision=%d error=%v", update.UpdateID, update.Revision, buildErr)
			if firstErr == nil {
				firstErr = buildErr
			}
			continue
		}
		result, oneErr := downloadAndInstall(ctx, session, singleCollection, singleRows, true, w.log)
		singleCleanup()
		if oneErr != nil && firstErr == nil {
			firstErr = oneErr
		}
		w.log("install wua_individual_complete update_id=%s revision=%d result=%s hresult=%s reboot_required=%t error=%v", update.UpdateID, update.Revision, result.ResultCode, result.HResult, result.RebootRequired, oneErr)
		individual.RebootRequired = individual.RebootRequired || result.RebootRequired
		individual.Results = append(individual.Results, result.Results...)
		if result.HResult != "" {
			individual.HResult = result.HResult
		}
	}
	individual.FinishedAt = time.Now().Unix()
	if firstErr != nil {
		individual.ResultCode = "failed"
	}
	w.log("install wua_individual_retry_complete result=%s hresult=%s reboot_required=%t results=%d error=%v", individual.ResultCode, individual.HResult, individual.RebootRequired, len(individual.Results), firstErr)
	return individual, firstErr
}

func (wuaCOMAdapter) PendingReboot(context.Context) (bool, error) {
	rebootKeys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`},
	}
	for _, candidate := range rebootKeys {
		key, err := registry.OpenKey(candidate.root, candidate.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if strings.Contains(candidate.path, `Session Manager`) {
			values, _, readErr := key.GetStringsValue("PendingFileRenameOperations")
			key.Close()
			if readErr == nil && len(values) > 0 {
				return true, nil
			}
			continue
		}
		key.Close()
		return true, nil
	}
	return false, nil
}

func (wuaCOMAdapter) Reboot(ctx context.Context, delaySeconds int, comment string) error {
	return runShutdown(ctx, delaySeconds, comment)
}

func newUpdateSession() (*ole.IDispatch, func(), error) {
	if err := ole.CoInitialize(0); err != nil && hresult(err) != 1 {
		return nil, func() {}, fmt.Errorf("initialize COM: %w", err)
	}
	unknown, err := oleutil.CreateObject("Microsoft.Update.Session")
	if err != nil {
		ole.CoUninitialize()
		return nil, func() {}, fmt.Errorf("create Microsoft.Update.Session: %w", err)
	}
	dispatch, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		ole.CoUninitialize()
		return nil, func() {}, fmt.Errorf("query update session dispatch: %w", err)
	}
	cleanup := func() {
		dispatch.Release()
		unknown.Release()
		ole.CoUninitialize()
	}
	return dispatch, cleanup, nil
}

func searchUpdateCollection(session *ole.IDispatch) (*ole.IDispatch, func(), error) {
	searcherVar, err := oleutil.CallMethod(session, "CreateUpdateSearcher")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create update searcher: %w", err)
	}
	searcher := searcherVar.ToIDispatch()
	if searcher == nil {
		searcherVar.Clear()
		return nil, func() {}, fmt.Errorf("create update searcher returned no dispatch")
	}
	searchResultVar, err := oleutil.CallMethod(searcher, "Search", "IsInstalled=0")
	if err != nil {
		searcherVar.Clear()
		return nil, func() {}, fmt.Errorf("search Windows updates: %w", err)
	}
	searchResult := searchResultVar.ToIDispatch()
	if searchResult == nil {
		searchResultVar.Clear()
		searcherVar.Clear()
		return nil, func() {}, fmt.Errorf("search Windows updates returned no dispatch")
	}
	updatesVar, err := oleutil.GetProperty(searchResult, "Updates")
	if err != nil {
		searchResultVar.Clear()
		searcherVar.Clear()
		return nil, func() {}, fmt.Errorf("read update collection: %w", err)
	}
	updates := updatesVar.ToIDispatch()
	if updates == nil {
		updatesVar.Clear()
		searchResultVar.Clear()
		searcherVar.Clear()
		return nil, func() {}, fmt.Errorf("read update collection returned no dispatch")
	}
	cleanup := func() {
		updatesVar.Clear()
		searchResultVar.Clear()
		searcherVar.Clear()
	}
	return updates, cleanup, nil
}

func updatesFromCollection(collection *ole.IDispatch) ([]Update, error) {
	if collection == nil {
		return nil, fmt.Errorf("nil update collection")
	}
	countVar, err := oleutil.GetProperty(collection, "Count")
	if err != nil {
		return nil, err
	}
	defer countVar.Clear()
	count := int(countVar.Val)
	updates := make([]Update, 0, count)
	for index := 0; index < count; index++ {
		updateVar, err := oleutil.GetProperty(collection, "Item", index)
		if err != nil {
			return nil, err
		}
		update := updateVar.ToIDispatch()
		if update == nil {
			updateVar.Clear()
			continue
		}
		row := updateFromDispatch(update)
		updateVar.Clear()
		if row.UpdateID != "" {
			updates = append(updates, row)
		}
	}
	sort.Slice(updates, func(i, j int) bool {
		return strings.ToLower(updates[i].Title) < strings.ToLower(updates[j].Title)
	})
	return updates, nil
}

func updateFromDispatch(update *ole.IDispatch) Update {
	identity, cleanupIdentity := dispatchProperty(update, "Identity")
	defer cleanupIdentity()
	row := Update{
		UpdateID:       getStringProperty(identity, "UpdateID"),
		Revision:       int(getIntProperty(identity, "RevisionNumber")),
		Title:          getStringProperty(update, "Title"),
		Description:    getStringProperty(update, "Description"),
		UpdateType:     getStringProperty(update, "Type"),
		MsrcSeverity:   getStringProperty(update, "MsrcSeverity"),
		SupportURL:     getStringProperty(update, "SupportUrl"),
		SizeBytes:      getIntProperty(update, "MaxDownloadSize"),
		IsInstalled:    getBoolProperty(update, "IsInstalled"),
		IsDownloaded:   getBoolProperty(update, "IsDownloaded"),
		IsHidden:       getBoolProperty(update, "IsHidden"),
		RequiresReboot: getBoolProperty(update, "RebootRequired"),
		Source:         "windows_update_agent",
	}
	row.KBArticleIDs = stringCollection(update, "KBArticleIDs")
	row.Categories, row.CategoryIDs, row.Classifications = categoryCollection(update, "Categories")
	return row
}

func downloadAndInstall(ctx context.Context, session *ole.IDispatch, collection *ole.IDispatch, updates []Update, individual bool, log wuaLogger) (InstallSummary, error) {
	startedAt := time.Now().Unix()
	summary := InstallSummary{StartedAt: startedAt, ResultCode: "running"}
	mode := "batch"
	if individual {
		mode = "individual"
	}
	select {
	case <-ctx.Done():
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "cancelled"
		return summary, ctx.Err()
	default:
	}
	logWUA(log, "install wua_eula_accept_start mode=%s updates=%d", mode, len(updates))
	acceptedEULAs, err := acceptEULAs(collection)
	if err != nil {
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "failed"
		summary.HResult = fmt.Sprintf("%#x", hresult(err))
		summary.Results = markResults(updates, "eula_failed", summary.HResult)
		logWUA(log, "install wua_eula_accept_failed mode=%s hresult=%s error=%v", mode, summary.HResult, err)
		return summary, err
	}
	logWUA(log, "install wua_eula_accept_complete mode=%s accepted=%d", mode, acceptedEULAs)
	logWUA(log, "install wua_downloader_create_start mode=%s", mode)
	downloaderVar, err := oleutil.CallMethod(session, "CreateUpdateDownloader")
	if err != nil {
		logWUA(log, "install wua_downloader_create_failed mode=%s error=%v", mode, err)
		return summary, err
	}
	defer downloaderVar.Clear()
	downloader := downloaderVar.ToIDispatch()
	if _, err = oleutil.PutProperty(downloader, "Updates", collection); err != nil {
		logWUA(log, "install wua_downloader_set_updates_failed mode=%s error=%v", mode, err)
		return summary, err
	}
	logWUA(log, "install wua_download_start mode=%s updates=%d", mode, len(updates))
	if _, err = oleutil.CallMethod(downloader, "Download"); err != nil {
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "failed"
		summary.HResult = fmt.Sprintf("%#x", hresult(err))
		summary.Results = markResults(updates, "download_failed", summary.HResult)
		logWUA(log, "install wua_download_failed mode=%s hresult=%s error=%v", mode, summary.HResult, err)
		return summary, err
	}
	logWUA(log, "install wua_download_complete mode=%s updates=%d", mode, len(updates))
	logWUA(log, "install wua_installer_create_start mode=%s", mode)
	installerVar, err := oleutil.CallMethod(session, "CreateUpdateInstaller")
	if err != nil {
		logWUA(log, "install wua_installer_create_failed mode=%s error=%v", mode, err)
		return summary, err
	}
	defer installerVar.Clear()
	installer := installerVar.ToIDispatch()
	if _, err = oleutil.PutProperty(installer, "Updates", collection); err != nil {
		logWUA(log, "install wua_installer_set_updates_failed mode=%s error=%v", mode, err)
		return summary, err
	}
	logWUA(log, "install wua_install_start mode=%s updates=%d", mode, len(updates))
	resultVar, err := oleutil.CallMethod(installer, "Install")
	if err != nil {
		summary.FinishedAt = time.Now().Unix()
		summary.ResultCode = "failed"
		summary.HResult = fmt.Sprintf("%#x", hresult(err))
		summary.Results = markResults(updates, "install_failed", summary.HResult)
		logWUA(log, "install wua_install_failed mode=%s hresult=%s error=%v", mode, summary.HResult, err)
		return summary, err
	}
	defer resultVar.Clear()
	result := resultVar.ToIDispatch()
	summary.FinishedAt = time.Now().Unix()
	summary.RebootRequired = getBoolProperty(result, "RebootRequired")
	summary.ResultCode = mapOperationResultCode(getIntProperty(result, "ResultCode"))
	summary.HResult = fmt.Sprintf("%#x", getIntProperty(result, "HResult"))
	summary.Results = updateResultRows(result, updates, summary.HResult)
	if individual && len(summary.Results) == 0 {
		summary.Results = markResults(updates, summary.ResultCode, summary.HResult)
	}
	logWUA(log, "install wua_install_complete mode=%s result=%s hresult=%s reboot_required=%t results=%d", mode, summary.ResultCode, summary.HResult, summary.RebootRequired, len(summary.Results))
	return summary, nil
}

func logWUA(log wuaLogger, format string, args ...any) {
	if log != nil {
		log(format, args...)
	}
}

func acceptEULAs(collection *ole.IDispatch) (int, error) {
	if collection == nil {
		return 0, fmt.Errorf("nil update collection")
	}
	count := int(getIntProperty(collection, "Count"))
	accepted := 0
	for index := 0; index < count; index++ {
		itemVar, err := oleutil.GetProperty(collection, "Item", index)
		if err != nil {
			return accepted, fmt.Errorf("read update %d for EULA acceptance: %w", index, err)
		}
		update := itemVar.ToIDispatch()
		if update == nil {
			itemVar.Clear()
			continue
		}
		if !getBoolProperty(update, "EulaAccepted") {
			acceptedVar, err := oleutil.CallMethod(update, "AcceptEula")
			if acceptedVar != nil {
				acceptedVar.Clear()
			}
			if err != nil {
				itemVar.Clear()
				return accepted, fmt.Errorf("accept update %d EULA: %w", index, err)
			}
			accepted++
		}
		itemVar.Clear()
	}
	return accepted, nil
}

func newUpdateCollection() (*ole.IDispatch, func(), error) {
	unknown, err := oleutil.CreateObject("Microsoft.Update.UpdateColl")
	if err != nil {
		return nil, func() {}, err
	}
	collection, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		unknown.Release()
		return nil, func() {}, err
	}
	cleanup := func() {
		collection.Release()
		unknown.Release()
	}
	return collection, cleanup, nil
}

func buildSelectedUpdateCollection(source *ole.IDispatch, selected map[string]Update) (*ole.IDispatch, func(), []Update, error) {
	collection, cleanup, err := newUpdateCollection()
	if err != nil {
		return nil, func() {}, nil, err
	}
	count := int(getIntProperty(source, "Count"))
	rows := []Update{}
	for index := 0; index < count; index++ {
		itemVar, err := oleutil.GetProperty(source, "Item", index)
		if err != nil {
			continue
		}
		update := itemVar.ToIDispatch()
		if update == nil {
			itemVar.Clear()
			continue
		}
		row := updateFromDispatch(update)
		if _, ok := selected[updateKey(row)]; ok {
			if _, addErr := oleutil.CallMethod(collection, "Add", update); addErr == nil {
				rows = append(rows, row)
			}
		}
		itemVar.Clear()
	}
	return collection, cleanup, rows, nil
}

func markResults(updates []Update, resultCode string, hresult string) []Update {
	out := make([]Update, 0, len(updates))
	for _, update := range updates {
		next := update
		next.ResultCode = resultCode
		next.HResult = hresult
		out = append(out, next)
	}
	return out
}

func updateResultRows(result *ole.IDispatch, updates []Update, fallbackHRESULT string) []Update {
	if result == nil {
		return markResults(updates, "unknown", fallbackHRESULT)
	}
	out := make([]Update, 0, len(updates))
	for index, update := range updates {
		next := update
		itemVar, err := oleutil.CallMethod(result, "GetUpdateResult", index)
		if err != nil {
			next.ResultCode = "unknown"
			next.HResult = fallbackHRESULT
			out = append(out, next)
			continue
		}
		item := itemVar.ToIDispatch()
		next.ResultCode = mapOperationResultCode(getIntProperty(item, "ResultCode"))
		next.HResult = fmt.Sprintf("%#x", getIntProperty(item, "HResult"))
		next.RequiresReboot = getBoolProperty(item, "RebootRequired")
		itemVar.Clear()
		out = append(out, next)
	}
	return out
}

func stringCollection(parent *ole.IDispatch, property string) []string {
	collection, cleanup := dispatchProperty(parent, property)
	if collection == nil {
		return nil
	}
	defer cleanup()
	count := int(getIntProperty(collection, "Count"))
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		itemVar, err := oleutil.GetProperty(collection, "Item", index)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(itemVar.ToString())
		if text != "" {
			values = append(values, text)
		}
		itemVar.Clear()
	}
	return values
}

func categoryCollection(parent *ole.IDispatch, property string) ([]string, []string, []string) {
	collection, cleanup := dispatchProperty(parent, property)
	if collection == nil {
		return nil, nil, nil
	}
	defer cleanup()
	count := int(getIntProperty(collection, "Count"))
	names := []string{}
	ids := []string{}
	classes := []string{}
	for index := 0; index < count; index++ {
		itemVar, err := oleutil.GetProperty(collection, "Item", index)
		if err != nil {
			continue
		}
		category := itemVar.ToIDispatch()
		name := getStringProperty(category, "Name")
		id := getStringProperty(category, "CategoryID")
		categoryType := getStringProperty(category, "Type")
		if name != "" {
			names = append(names, name)
		}
		if id != "" {
			ids = append(ids, id)
		}
		if strings.EqualFold(categoryType, "UpdateClassification") && name != "" {
			classes = append(classes, name)
		}
		itemVar.Clear()
	}
	return names, ids, classes
}

func dispatchProperty(parent *ole.IDispatch, name string) (*ole.IDispatch, func()) {
	if parent == nil {
		return nil, func() {}
	}
	value, err := oleutil.GetProperty(parent, name)
	if err != nil {
		return nil, func() {}
	}
	return value.ToIDispatch(), func() {
		value.Clear()
	}
}

func getStringProperty(parent *ole.IDispatch, name string) string {
	if parent == nil {
		return ""
	}
	value, err := oleutil.GetProperty(parent, name)
	if err != nil {
		return ""
	}
	defer value.Clear()
	return strings.TrimSpace(fmt.Sprint(value.Value()))
}

func getBoolProperty(parent *ole.IDispatch, name string) bool {
	if parent == nil {
		return false
	}
	value, err := oleutil.GetProperty(parent, name)
	if err != nil {
		return false
	}
	defer value.Clear()
	typed, ok := value.Value().(bool)
	if ok {
		return typed
	}
	return value.Val != 0
}

func getIntProperty(parent *ole.IDispatch, name string) int64 {
	if parent == nil {
		return 0
	}
	value, err := oleutil.GetProperty(parent, name)
	if err != nil {
		return 0
	}
	defer value.Clear()
	switch typed := value.Value().(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return int64(value.Val)
	}
}

func mapOperationResultCode(code int64) string {
	switch code {
	case 0:
		return "not_started"
	case 1:
		return "in_progress"
	case 2:
		return "success"
	case 3:
		return "success_with_errors"
	case 4:
		return "failed"
	case 5:
		return "aborted"
	default:
		return fmt.Sprintf("unknown_%d", code)
	}
}

func updateKey(update Update) string {
	return strings.ToLower(strings.TrimSpace(update.UpdateID)) + ":" + fmt.Sprint(update.Revision)
}

func hresult(err error) int64 {
	if err == nil {
		return 0
	}
	var oleErr *ole.OleError
	if errors.As(err, &oleErr) {
		return int64(oleErr.Code())
	}
	return 0
}

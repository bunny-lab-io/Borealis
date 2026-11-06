package main

import (
	"strconv"
	"strings"
)

const (
	siteWorkerNamePrefix        = "site-worker-"
	siteWorkerKubernetesNameMax = 63
	siteWorkerSiteSlugMax       = siteWorkerKubernetesNameMax - len(siteWorkerNamePrefix)
)

// Primary Pods share the immutable site reservation name. A delayed CREATE
// cannot bypass a replacement after its original Service was retired.
func siteWorkerPodNameForSite(siteID int64) string {
	if siteID <= 0 {
		return ""
	}
	return siteWorkerNamePrefix + "site-" + strconv.FormatInt(siteID, 10)
}

// The Service CREATE reserves launch before the Pod CREATE. Site name and
// worker identity remain labels/annotations, never distinct reservation keys.
func siteWorkerServiceNameForSite(siteID int64, siteName string, workerGUID string) string {
	return siteWorkerPodNameForSite(siteID)
}

func siteWorkerNameForSite(siteID int64, siteName string, workerGUID string) string {
	slug := siteWorkerSiteSlug(siteID, siteName)
	if len(slug) > siteWorkerSiteSlugMax {
		slug = strings.Trim(slug[:siteWorkerSiteSlugMax], "-")
	}
	if slug != "" {
		return siteWorkerNamePrefix + slug
	}

	workerGUID = strings.ToLower(cleanText(workerGUID))
	if workerGUID != "" {
		return siteWorkerNamePrefix + workerGUID
	}
	return strings.TrimSuffix(siteWorkerNamePrefix, "-")
}

func siteWorkerSiteSlug(siteID int64, siteName string) string {
	normalized := strings.ToLower(strings.TrimSpace(siteName))
	var builder strings.Builder
	lastWasSeparator := false
	for _, item := range normalized {
		switch {
		case item >= 'a' && item <= 'z':
			builder.WriteRune(item)
			lastWasSeparator = false
		case item >= '0' && item <= '9':
			builder.WriteRune(item)
			lastWasSeparator = false
		case item == ' ' || item == '\t' || item == '\n' || item == '\r' || item == '-' || item == '_':
			if builder.Len() > 0 && !lastWasSeparator {
				builder.WriteByte('-')
				lastWasSeparator = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug != "" {
		return slug
	}
	if siteID > 0 {
		return "site-" + strconv.FormatInt(siteID, 10)
	}
	return ""
}

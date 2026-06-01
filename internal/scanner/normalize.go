package scanner

import "strings"

func NormalizeScanResult(result ScanResult) ScanResult {
	if result.Pages == nil {
		result.Pages = []PageResult{}
	}
	if result.Blocks == nil {
		result.Blocks = []BlockStat{}
	}
	if result.Sections == nil {
		result.Sections = []SectionStat{}
	}
	for i := range result.Pages {
		result.Pages[i] = NormalizePage(result.Pages[i])
	}
	for i := range result.Blocks {
		if result.Blocks[i].Variations == nil {
			result.Blocks[i].Variations = map[string]int{}
		}
		if result.Blocks[i].Pages == nil {
			result.Blocks[i].Pages = []string{}
		}
	}
	for i := range result.Sections {
		if result.Sections[i].Pages == nil {
			result.Sections[i].Pages = []string{}
		}
	}
	return result
}

func NormalizePage(page PageResult) PageResult {
	if page.Links == nil {
		page.Links = []LinkInfo{}
	}
	if page.Blocks == nil {
		page.Blocks = []BlockInfo{}
	}
	if page.Sections == nil {
		page.Sections = []SectionInfo{}
	}
	if page.AuditStatus == "" {
		switch {
		case page.AuditError != "":
			page.AuditStatus = "failed"
		case page.Lighthouse.Health != nil:
			page.AuditStatus = "complete"
		default:
			page.AuditStatus = "pending"
		}
	}
	if page.AuditStatus == "failed" && isCanceledAuditError(page.AuditError) {
		page.AuditStatus = "pending"
		page.AuditError = ""
	}
	for i := range page.Blocks {
		if page.Blocks[i].Variations == nil {
			page.Blocks[i].Variations = []string{}
		}
	}
	for i := range page.Sections {
		if page.Sections[i].Variations == nil {
			page.Sections[i].Variations = []string{}
		}
		if page.Sections[i].Blocks == nil {
			page.Sections[i].Blocks = []string{}
		}
	}
	return page
}

func isCanceledAuditError(value string) bool {
	return strings.Contains(strings.ToLower(value), "context canceled")
}

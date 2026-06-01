package scanner

import (
	"io"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

func AnalyzeHTML(pageURL string, body io.Reader, root *url.URL) (PageResult, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return PageResult{}, err
	}
	pageBase, err := url.Parse(pageURL)
	if err != nil {
		pageBase = root
	}

	page := PageResult{URL: pageURL}
	page.Title = textOfFirst(doc, "title")
	page.H1 = textOfFirst(doc, "h1")
	page.Canonical = firstLinkRel(doc, "canonical")
	page.Description = firstMeta(doc, "name", "description")
	page.Robots = firstMeta(doc, "name", "robots")
	page.Lang = attr(findFirst(doc, "html"), "lang")
	page.OG = OpenGraph{
		Title:       firstMeta(doc, "property", "og:title"),
		Description: firstMeta(doc, "property", "og:description"),
		Image:       firstMeta(doc, "property", "og:image"),
		URL:         firstMeta(doc, "property", "og:url"),
		Type:        firstMeta(doc, "property", "og:type"),
		SiteName:    firstMeta(doc, "property", "og:site_name"),
	}

	page.Links = extractLinks(doc, pageBase, root)
	for _, link := range page.Links {
		switch link.Kind {
		case "internal":
			page.InternalLinks++
		case "external":
			page.ExternalLinks++
		}
	}
	page.LinkCount = len(page.Links)

	page.Sections, page.Blocks = extractEDS(doc)
	page.SectionCount = len(page.Sections)
	page.BlockCount = len(page.Blocks)
	return NormalizePage(page), nil
}

func extractEDS(doc *html.Node) ([]SectionInfo, []BlockInfo) {
	main := findFirst(doc, "main")
	if main == nil {
		return []SectionInfo{}, []BlockInfo{}
	}

	sections := []SectionInfo{}
	blocks := []BlockInfo{}
	sectionIndex := 0
	for child := main.FirstChild; child != nil; child = child.NextSibling {
		if !isElement(child, "div") {
			continue
		}
		sectionIndex++
		section := SectionInfo{Index: sectionIndex, Variations: []string{}, Blocks: []string{}}
		variations := make(map[string]bool)
		for _, className := range classList(child) {
			if className != "section" {
				variations[className] = true
			}
		}

		for blockNode := child.FirstChild; blockNode != nil; blockNode = blockNode.NextSibling {
			if !isElement(blockNode, "div") {
				continue
			}
			classes := classList(blockNode)
			if isSectionMetadata(classes) {
				for _, variation := range metadataVariations(blockNode) {
					variations[variation] = true
				}
				continue
			}
			if len(classes) == 0 {
				continue
			}
			name := classes[0]
			blockVariations := []string{}
			for _, variation := range classes[1:] {
				if variation != "" && variation != "block" {
					blockVariations = append(blockVariations, variation)
				}
			}
			sort.Strings(blockVariations)
			blocks = append(blocks, BlockInfo{Name: name, Variations: blockVariations, SectionIndex: sectionIndex})
			section.Blocks = append(section.Blocks, name)
		}

		for variation := range variations {
			section.Variations = append(section.Variations, variation)
		}
		sort.Strings(section.Variations)
		sections = append(sections, section)
	}
	return sections, blocks
}

func metadataVariations(n *html.Node) []string {
	var values []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if isElement(node, "div") {
			texts := directChildTexts(node)
			if len(texts) >= 2 {
				key := normalizeToken(texts[0])
				if key == "style" || key == "styles" || key == "class" || key == "classes" {
					for _, value := range strings.Fields(strings.ReplaceAll(texts[1], ",", " ")) {
						values = append(values, normalizeToken(value))
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return values
}

func directChildTexts(n *html.Node) []string {
	var texts []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			value := strings.TrimSpace(child.Data)
			if value != "" {
				texts = append(texts, value)
			}
			continue
		}
		if child.Type == html.ElementNode {
			value := strings.TrimSpace(nodeText(child))
			if value != "" {
				texts = append(texts, value)
			}
		}
	}
	return texts
}

func extractLinks(doc *html.Node, pageBase *url.URL, root *url.URL) []LinkInfo {
	links := []LinkInfo{}
	walk(doc, func(n *html.Node) {
		if !isElement(n, "a") {
			return
		}
		href := strings.TrimSpace(attr(n, "href"))
		if href == "" {
			return
		}
		parsed, err := url.Parse(href)
		if err != nil {
			return
		}
		resolved := pageBase.ResolveReference(parsed)
		resolved.Fragment = ""
		kind := classifyLink(href, resolved, root)
		links = append(links, LinkInfo{
			Href:     href,
			URL:      resolved.String(),
			Text:     compactText(nodeText(n)),
			Target:   attr(n, "target"),
			Rel:      attr(n, "rel"),
			Kind:     kind,
			External: kind == "external",
		})
	})
	return links
}

func firstMeta(doc *html.Node, keyAttr, keyValue string) string {
	keyValue = strings.ToLower(keyValue)
	var value string
	walk(doc, func(n *html.Node) {
		if value != "" || !isElement(n, "meta") {
			return
		}
		if strings.EqualFold(attr(n, keyAttr), keyValue) {
			value = attr(n, "content")
		}
	})
	return strings.TrimSpace(value)
}

func firstLinkRel(doc *html.Node, rel string) string {
	var value string
	walk(doc, func(n *html.Node) {
		if value != "" || !isElement(n, "link") {
			return
		}
		if strings.EqualFold(attr(n, "rel"), rel) {
			value = attr(n, "href")
		}
	})
	return strings.TrimSpace(value)
}

func textOfFirst(doc *html.Node, tag string) string {
	node := findFirst(doc, tag)
	if node == nil {
		return ""
	}
	return compactText(nodeText(node))
}

func findFirst(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if isElement(n, tag) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirst(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func walk(n *html.Node, fn func(*html.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walk(child, fn)
	}
}

func isElement(n *html.Node, tag string) bool {
	return n != nil && n.Type == html.ElementNode && strings.EqualFold(n.Data, tag)
}

func attr(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func classList(n *html.Node) []string {
	raw := attr(n, "class")
	fields := strings.Fields(raw)
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if normalized := normalizeToken(field); normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

func isSectionMetadata(classes []string) bool {
	if len(classes) == 0 {
		return false
	}
	first := classes[0]
	return first == "section-metadata" || first == "metadata"
}

func normalizeToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.Trim(value, ".#")
	return value
}

func nodeText(n *html.Node) string {
	var builder strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			builder.WriteString(node.Data)
			builder.WriteString(" ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return builder.String()
}

func compactText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

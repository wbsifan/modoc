package cmd

import (
	"github.com/wbsifan/modoc/asset"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/wbsifan/modoc/helper"

	"github.com/wbsifan/modoc/model"

	"github.com/PuerkitoBio/goquery"
	"github.com/flosch/pongo2"
	"github.com/mozillazg/go-slugify"
	mdparser "github.com/russross/blackfriday"
	"github.com/spf13/cobra"
)

type (
	Toc struct {
		Title string
		Link  string
		Child []*Toc
	}
)

var (
	bodyTpl  *pongo2.Template
	search   *model.Search
	index    *model.Node
	theme    *model.Theme
	skin     *model.Skin
	pinyin   = make(map[string]int)
	nodeList = make([]*model.Node, 0)
	buildCmd = &cobra.Command{
		Use:   "build",
		Short: "Building HTML site",
		Long:  `Building HTML site`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runBuild()
		},
	}
)

func init() {
	rootCmd.AddCommand(buildCmd)
}

func runBuild() {
	loadConfig()
	loadNav()
	loadTpl()
	initTheme()
	initSearch()
	initNode(nav)
	makeNode()
	makeStatic()
	if cfg.Search {
		makeSearch()
	}
}

func loadTpl() {
	var err error
	bodyTplFile := filepath.Join(cfg.Theme, "body.html")
	bodyTpl, err = asset.Tpl.FromFile(bodyTplFile)
	if err != nil {
		log.Fatal(err)
	}
}

func initSearch() {
	search = &model.Search{
		Config: &model.SearchConfig{
			Lang:          []string{"en"},
			PrebuildIndex: false,
			Separator:     "[\\s\\-]+",
		},
	}
}

func initTheme() {
	theme = model.NewTheme()
	f := filepath.Join(cfg.Theme, "theme.yaml")
	cbyte, err := asset.Box.Find(f)
	if err != nil {
		log.Fatal(err)
	}
	err = yaml.Unmarshal(cbyte, theme)
	if err != nil {
		log.Fatal(err)
	}
	s, has := theme.Skin[cfg.Skin]
	if !has {
		log.Fatal("No skin in `theme.yaml`:", cfg.Skin)
	}
	skin = s
}

func makeSearch() {
	cbyte, _ := json.Marshal(search)
	dstPath := filepath.Join(cfg.SiteDir, "static/search/search_index.json")
	err := helper.WriteFile(dstPath, string(cbyte))
	if err != nil {
		log.Fatal(err)
	}
}

func makeStatic() {
	srcPath := filepath.Join(cfg.Theme, "static")
	fileList, err := asset.FileList(srcPath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("build: make static")
	for _, path := range fileList {
		file, _ := filepath.Rel(srcPath, path)
		dst := filepath.Join(cfg.SiteDir, "static", file)
		str, err := asset.Box.FindString(path) 
		if err != nil {
			log.Fatal(err)
		}
		_ = helper.WriteFile(dst, str)
	}
}

func initNode(node *model.Node) {
	node.Init()
	// 首页
	if node.IsIndex {
		index = node
	}
	// 链接里的汉字转成拼音
	if cfg.LinkPinyin && node.Link != "" {
		links := strings.Split(node.Link, "/")
		var slug []string
		for _, v := range links {
			slug = append(slug, slugify.Slugify(v))
		}
		newlink := strings.Join(slug, "/")
		_, has := pinyin[newlink]
		if has {
			pinyin[newlink] += 1
			newlink = fmt.Sprintf("%s-%v", newlink, pinyin[newlink])
		} else {
			pinyin[newlink] = 1
		}
		node.Link = newlink
	}
	// 如果是文件加入到列表
	if node.IsFile {
		nodeList = append(nodeList, node)
	}
	for _, n := range node.Child {
		n.Parent = node
		initNode(n)
	}
}

func makeNode() {
	max := len(nodeList) - 1
	for i, node := range nodeList {
		node.SetActive(true)
		// 上一页下一页
		prevId := i - 1
		nextId := i + 1
		if prevId < 0 {
			prevId = max
		}
		if nextId > max {
			nextId = 0
		}
		node.Prev = nodeList[prevId]
		node.Next = nodeList[nextId]
		makeBody(node)
		node.SetActive(false)
	}
}

func makeBody(node *model.Node) {
	srcPath := filepath.Join(cfg.DocsDir, node.Path)
	dstPath := filepath.Join(cfg.SiteDir, node.Link, "index.html")
	if node.IsIndex {
		dstPath = filepath.Join(cfg.SiteDir, "index.html")
	}
	output := parseMd(srcPath)
	html, text, tocs := parseToc(output)
	// Add search doc
	doc := &model.SearchDoc{
		Location: node.Link,
		Text:     text,
		Title:    node.Title,
	}
	search.AddDoc(doc)
	out, err := bodyTpl.Execute(pongo2.Context{
		"config":  cfg,
		"nav":     nav,
		"tocs":    tocs,
		"content": html,
		"node":    node,
		"index":   index,
		"theme":   theme,
		"skin":    skin,
		"baseDir": node.BaseDir,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("build:", dstPath)
	err = helper.WriteFile(dstPath, out)
	if err != nil {
		log.Fatal(err)
	}
}

func parseMd(path string) []byte {
	input, _ := ioutil.ReadFile(path)
	renderer := mdparser.NewHTMLRenderer(mdparser.HTMLRendererParameters{
		Flags: mdparser.CommonHTMLFlags | mdparser.TOC,
	})
	output := mdparser.Run(input, mdparser.WithRenderer(renderer))
	return output
}

func parseToc(input []byte) (string, string, *Toc) {
	tocs := &Toc{}
	buf := bytes.NewReader(input)
	dom, err := goquery.NewDocumentFromReader(buf)
	if err != nil {
		log.Fatal(err)
	}
	// Find h1 h2
	dom.Find("nav>ul>li").Each(func(i int, li *goquery.Selection) {
		a := li.Find("nav>ul>li>a")
		link, has := a.Attr("href")
		h1 := &Toc{
			Title: a.Text(),
			Link:  link,
		}
		if has {
			tocs.Child = append(tocs.Child, h1)
		}
		li.Find("nav>ul>li>ul>li>a").Each(func(j int, a *goquery.Selection) {
			link, _ := a.Attr("href")
			h2 := &Toc{
				Title: a.Text(),
				Link:  link,
			}
			if has {
				h1.Child = append(h1.Child, h2)
			} else {
				tocs.Child = append(tocs.Child, h2)
			}
		})
	})
	// Remove nav
	dom.Find("nav").Remove()
	html, err := dom.Html()
	if err != nil {
		log.Println(err)
	}
	text := strings.Replace(dom.Text(), "\n\n", "", -1)
	return html, text, tocs
}

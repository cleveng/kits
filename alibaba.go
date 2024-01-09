package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	dir          = "alibaba"
	descURL      = "https://www.alibaba.com/event/app/mainAction/desc.htm?detailId=%s&language=en"
	detailPrefix = "https://www.alibaba.com/product-detail/"
)

func init() {
	os.Mkdir(dir, 0777)
}

type Alibaba struct {
	client *http.Client
}

type Crawler interface {
	GetURL(wg *sync.WaitGroup, url string) error
	GetDetail(id string) (string, error)
}

func NewAlibaba() Crawler {
	return &Alibaba{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (a *Alibaba) GetDetail(id string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf(descURL, id), nil)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("status code error: " + resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var sp map[string]any
	err = json.Unmarshal(body, &sp)
	if err != nil {
		return "", err
	}

	data := sp["data"].(map[string]any)

	content := data["productHtmlDescription"].(string)
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader([]byte(content)))
	if err != nil {
		return "", err
	}

	doc.Find("IMG,img").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("data-src")
		s.SetAttr("src", src)
	})

	return doc.Html()
}

func (a *Alibaba) GetURL(wg *sync.WaitGroup, rawURL string) error {
	defer wg.Done()

	rawURL = strings.Replace(rawURL, detailPrefix, "", -1)
	rawURL = url.QueryEscape(rawURL)

	fmt.Printf("%s >>> 正在获取", rawURL)
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s%s", detailPrefix, rawURL), nil)

	// curl -I rawURL
	req.Header.Add("Cookie", "**")
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("status code error: " + resp.Status)
	}

	var (
		sp map[string]any
	)
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		panic(err)
	}

	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		content := s.Text()
		if len(content) == 0 || !strings.Contains(content, "window.detailData") {
			return
		}

		// 使用正则表达式提取 JSON 数据
		re := regexp.MustCompile(`window\.detailData\s*=\s*(.*)`)
		matches := re.FindStringSubmatch(content)
		if len(matches) < 2 {
			panic("No JSON data found.")
		}

		if err = json.Unmarshal([]byte(matches[1]), &sp); err != nil {
			panic(err)
		}
	})

	globalData := sp["globalData"].(map[string]any)
	product := globalData["product"].(map[string]any)
	productId := fmt.Sprintf("%0.0f", product["productId"].(float64))
	targetDir := fmt.Sprintf("%s/%s", dir, productId)

	// 下载附件资源
	media := product["mediaItems"].([]any)
	for _, v := range media {
		medium := v.(map[string]any)
		switch medium["type"].(string) {
		case "video":
			var (
				video    = medium["videoUrl"].(map[string]any)
				videoUrl string
				sizes    = []string{"hd", "hd_265", "sd_265", "ld", "sd"}
			)
			for _, size := range sizes {
				if item, ok := video[size]; ok {
					videoUrl = item.(map[string]any)["videoUrl"].(string)
					break
				}
			}

			go downloadFile(videoUrl, targetDir)
		case "image":
			var (
				image    = medium["imageUrl"].(map[string]any)
				imageUrl string
				sizes    = []string{"big", "normal", "small", "thumb"}
			)
			for _, size := range sizes {
				if item, ok := image[size]; ok {
					imageUrl = item.(string)
					break
				}
			}
			go downloadFile(imageUrl, targetDir)
		}
	}

	// 保存标题 描述等参数信息
	os.Mkdir(targetDir, 0777)
	filename := fmt.Sprintf("%s/%s.txt", targetDir, productId)
	file, _ := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0755)
	subject := product["subject"].(string)
	keywords, _ := doc.Find("meta[name='keywords']").Attr("content")
	description, _ := doc.Find("meta[name='description']").Attr("content")

	// price
	var priceValue string
	price := product["price"].(map[string]any)
	if productLadderValue, ok := price["productLadderPrices"]; ok {
		productLadderPrices := productLadderValue.([]any)
		for _, v := range productLadderPrices {
			ladderPrice := v.(map[string]any)
			priceValue += fmt.Sprintf(">>> 数量: [%0.0f - %0.0f], 价格: %s\n", ladderPrice["min"].(float64), ladderPrice["max"].(float64), ladderPrice["formatPrice"].(string))
		}
	}

	var properties string
	if productKeyIndustryValue, ok := product["productKeyIndustryProperties"]; ok {
		for _, v := range productKeyIndustryValue.([]any) {
			mainProperty := v.(map[string]any)
			properties += fmt.Sprintf("主要属性: \n>>> %s: %s\n\n其他属性:\n", mainProperty["attrName"].(string), mainProperty["attrValue"].(string))
		}
	}

	for _, v := range product["productBasicProperties"].([]any) {
		basicProperty := v.(map[string]any)
		properties += fmt.Sprintf(">>> %s: %s\n", basicProperty["attrName"].(string), basicProperty["attrValue"].(string))
	}

	trade := globalData["trade"].(map[string]any)
	logisticInfo := trade["logisticInfo"].(map[string]any)
	var (
		packagingProperties        string
		productPackagingProperties = logisticInfo["productPackagingProperties"].([]any)
	)

	for _, v := range productPackagingProperties {
		packagingProperty := v.(map[string]any)
		packagingProperties += fmt.Sprintf(">>> %s: %s\n", packagingProperty["attrName"].(string), packagingProperty["attrValue"].(string))
	}

	var supplyAbility string
	if supplyAbilityValue, ok := logisticInfo["supplyAbility"]; ok {
		supplyAbility = supplyAbilityValue.(string)
	}

	var customizationList string
	for _, v := range product["productLightCustomizationList"].([]any) {
		lightCustomization := v.(map[string]any)
		customizationList += fmt.Sprintf(">>> 定制:%s, 最低起订量: %0.0f\n", lightCustomization["customType"].(string), lightCustomization["moq"].(float64))
	}

	content := fmt.Sprintf(`
标题: %s
关键词: %s
描述内容: %s

价格: 
%s
属性:
%s
包装与配送: %s
供应能力: %s
定制信息: %s
产品详情请查看 html 文件

`, subject, keywords, description, priceValue, properties, packagingProperties, supplyAbility, customizationList)

	file.WriteString(content)
	defer file.Close()

	// 产品详情内容
	desc, _ := os.OpenFile(fmt.Sprintf("%s/%s.html", targetDir, productId), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0755)
	defer desc.Close()
	if productDetail, err := a.GetDetail(productId); err == nil {
		desc.WriteString(productDetail)
	}

	fmt.Printf("%s >>> 下载完成", rawURL)
	return nil
}

func main() {
	var (
		urls = []string{
			"https://www.alibaba.com/product-detail/paint-boy-newest-wholesale-5d-diamond_1600155582218.html?spm=a2700.galleryofferlist.wending_right.6.6b614124IsHIQE",
		}
		cli = NewAlibaba()
	)

	var wg sync.WaitGroup
	for _, v := range urls {
		wg.Add(1)
		if err := cli.GetURL(&wg, v); err != nil {
			continue
		}
		time.Sleep(time.Second * 10)
	}
	wg.Wait()
}

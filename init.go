package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

/**
 * 下载文件
 */
func downloadFile(url, dirname string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 从URL中提取文件名
	fileName := filepath.Base(strings.Split(url, "?")[0])

	// 创建本地文件
	file, err := os.Create(fmt.Sprintf("%s/%s", dirname, fileName))
	if err != nil {
		return err
	}
	defer file.Close()

	// 将HTTP响应体的内容写入文件
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

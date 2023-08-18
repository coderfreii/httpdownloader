package main

import (
	"flag"
	"fmt"
	"os"
	"path"
)

func main() {
	url := flag.String("url", "", "URL of the file to download")
	output := flag.String("output", "", "Path to store the downloaded file (optional, default: current directory)")
	numThreads := flag.Int("threads", 10, "Number of threads for concurrent download (optional, default: 10)")
	checksum := flag.String("checksum", "", "Checksum algorithm (optional, e.g., md5, sha1) not support for now")
	filename := flag.String("filename", "", "Specify the name of the downloaded file (optional)")
	proxy := flag.String("proxy", "", "Specify the address of the proxy (optional)")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s -url <url> [options]\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Options:\n")
		flag.PrintDefaults()
	}

	var a []int
	a = append(a, 1)

	flag.Parse()

	if *filename == "" {
		*filename = path.Base(*url)
	}

	if *output == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			panic(fmt.Sprintf("Error while getting current working directory: %s", err))
		}
		*output = currentDir
	}

	if *url == "" {
		fmt.Println("URL is required. Usage: go run main.go -url <url> [other options]")
		return
	}

	dl, err := NewDownloader(*url, *output, *checksum, *filename, *numThreads, *proxy)
	if err != nil {
		fmt.Println("Error while creating the NewDownloader:", err)
		return
	}

	// 开始下载
	err = dl.Start()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	dl.printProgress()

	// 更新下载信息的显示
	dl.Update()
}

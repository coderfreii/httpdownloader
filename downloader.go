package main

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/cheggaaa/pb/v3"
	"hash"
	"httpdownloader/util"
	"io"
	"net"
	"net/http"
	goUrl "net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type downloadInfo struct {
	startOffset int64
	endOffset   int64
	bytesRead   int64
	startTime   int64
	retrying    bool
	dealt       int64
}

type Downloader struct {
	url           string
	filePath      string
	numThreads    int
	checksum      string
	filename      string
	fileSize      int64
	downloadInfos []downloadInfo
	hash          hash.Hash
	client        *http.Client
	wg            util.WaitGroupWithCount
	pbs           []*pb.ProgressBar
	file          *os.File // Added file field to store the file handle
	done          bool
}

func NewDownloader(url, filePath, checksum, filename string, numThreads int, proxyURL string) (*Downloader, error) {
	// 设置代理服务器的地址
	var parsedProxyURL *goUrl.URL = nil
	var err error
	if proxyURL != "" {
		parsedProxyURL, err = goUrl.Parse(proxyURL)
		if err != nil {
			panic(err)
		}
	}

	if parsedProxyURL != nil {
		fmt.Printf("proxy through %s", parsedProxyURL)
	}

	var dl *Downloader

	dl = &Downloader{
		url:        url,
		filePath:   filePath,
		numThreads: numThreads,
		checksum:   checksum,
		filename:   filename,
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxIdleConns:        numThreads,
				MaxIdleConnsPerHost: numThreads,
				Proxy:               http.ProxyURL(parsedProxyURL),
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				//update url and file name
				dl.filename = path.Base(req.URL.String())
				dl.url = req.URL.String()
				return nil
			},
		},
		done: false,
	}
	return dl, nil
}

func checkSupport(resp *http.Response) error {
	// Check if the server supports partial content (range requests)
	acceptRanges := resp.Header.Get("Accept-Ranges")

	if acceptRanges == "bytes" {
		return nil
	} else {
		return fmt.Errorf("server does not support partial content (range requests)")
	}
}

func (dl *Downloader) Start() error {
	// Send a HEAD request to the server
	resp, err := dl.client.Head(dl.url)
	if err != nil {
		return fmt.Errorf("error while sending HEAD request: %s", err)
	}
	defer resp.Body.Close()
	dl.checksum = resp.Header.Get("Content-Md5")

	firstCheckPass := true
	err = checkSupport(resp)
	if err != nil {
		firstCheckPass = false
	}

	// Get the total length of the file
	contentLength := resp.Header.Get("Content-Length")
	if contentLength != "" {
		fmt.Printf("Total length of the file: %s bytes\n", contentLength)
	} else {
		return fmt.Errorf("could not determine the total length of the file")
	}

	dl.fileSize = resp.ContentLength

	// 初始化用于计算校验和的哈希函数
	if dl.checksum != "" {
		dl.hash = md5.New()
	}

	// 计算每个 Goroutine 需要下载的数据块大小
	chunkSize := dl.fileSize / int64(dl.numThreads)

	// 定义一个存储下载信息的切片，用于显示进度和速度
	dl.downloadInfos = make([]downloadInfo, dl.numThreads)
	for i := 0; i < dl.numThreads; i++ {
		dl.downloadInfos[i].startOffset = int64(i) * chunkSize
		dl.downloadInfos[i].endOffset = dl.downloadInfos[i].startOffset + (chunkSize - 1)
		if i == dl.numThreads-1 {
			dl.downloadInfos[i].endOffset = dl.fileSize - 1 // 最后一个 Goroutine 可能会多下载一些，以确保下载全部数据
		}
	}

	if firstCheckPass != true {
		// 使用 HTTP Range 头部请求部分数据
		req, _err := http.NewRequest("GET", resp.Request.URL.String(), nil)
		if _err != nil {
			panic(errors.New(fmt.Sprintf("Error while creating request: %s", _err)))
		}
		//rangeHeader := fmt.Sprintf("bytes=%d-%d", dl.downloadInfos[0].startOffset, int(math.Min(float64(dl.downloadInfos[0].endOffset), 10)))
		rangeHeader := fmt.Sprintf("bytes=%d-%d", 0, 0)
		req.Header.Set("Range", rangeHeader)

		do, _err := dl.client.Do(req)
		if _err != nil {
			return _err
		}
		if do.StatusCode != 206 {
			return errors.New("不支持分段下载")
		}
		do.Body.Close()
		err = nil
	}

	// 启动多个 Goroutine 进行并发下载
	for i := 0; i < dl.numThreads; i++ {
		dl.wg.Add(1)
		go dl.downloadChunk(i)
	}

	defer func() {
		if _err := recover(); _err != nil {
			err = _err.(error)
		}
	}()
	return err
}

func (dl *Downloader) Update() {
	// 等待所有 Goroutine 完成
	dl.wg.Wait()
	dl.done = true

	// 将分块下载的文件合并为一个文件
	err := dl.mergeFiles()
	if err != nil {
		fmt.Println("Error while merging files:", err)
		return
	}

	// 输出下载文件的校验和
	if dl.checksum != "" {
		checksumData := hex.EncodeToString(dl.hash.Sum(nil))
		fmt.Printf("%s checksum of the downloaded file: %x\n", dl.checksum, checksumData)
	}
}

func (dl *Downloader) downloadChunk(threadNum int) {
	defer dl.wg.Done()

	partFileName := dl.filePath + "_" + strconv.Itoa(threadNum)
	file, err := os.Create(partFileName)
	defer file.Close()
	if err != nil {
		panic(errors.New("create file wrong"))
	}

	buf := make([]byte, 32*1024) // 32KB buffer

	info := &(dl.downloadInfos[threadNum])

	updateLeft := func() int64 {
		return (info.endOffset - info.startOffset + 1) - info.bytesRead
	}

	isComplete := func() bool {
		return updateLeft() == 0
	}

	// 使用 HTTP Range 头部请求部分数据
	req, err := http.NewRequest("GET", dl.url, nil)
	if err != nil {
		panic(errors.New(fmt.Sprintf("Error while creating request: %s", err)))
	}

	for {
		rangeHeader := fmt.Sprintf("bytes=%d-%d", info.startOffset+info.bytesRead, info.endOffset)
		req.Header.Set("Range", rangeHeader)
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Accept", "*/*")

		resp, err := dl.client.Do(req)
		if err != nil {
			if !info.retrying {
				info.retrying = true
			}
			// 等待一段时间后尝试重连
			time.Sleep(5 * time.Second)
			continue
		}

		//initial
		info.retrying = false
		atomic.StoreInt64(&info.startTime, time.Now().UnixNano())

		for {
			left := updateLeft()
			n, err := resp.Body.Read(buf)

			if err != nil && err != io.EOF {
				fmt.Println("Error while request:", err)
				break
			}

			//in case server gives more data result in file getting dirty
			if n > int(left) {
				println("remote server doesn't implement the protocol properly")
				n = int(left)
			}

			if left > 0 && n > 0 {
				_, _err := file.WriteAt(buf[:n], info.bytesRead)
				if _err != nil {
					panic(errors.New("error occur while writing in file"))
				}
				// update
				atomic.AddInt64(&info.bytesRead, int64(n))
			}

			left = updateLeft()

			if io.EOF == err {
				if left > 0 {
					panic("data is not enough")
				}

				if left == 0 {
					//println("end properly")
					break
				}
			} else {
				if left == 0 {
					println("remote server doesn't implement the protocol properly ( early end )")
					break
				}
			}
		}

		resp.Body.Close()

		// 判断下载是否完成
		if isComplete() {
			return
		} else {
			panic(errors.New("receive data from server is not enough"))
		}
	}
}

func (dl *Downloader) mergeFiles() error {
	file, err := os.Create(dl.filePath + "\\" + dl.filename)
	if err != nil {
		return err
	}
	defer file.Close()
	fmt.Println("merging files ...")

	for i := 0; i < dl.numThreads; i++ {
		partFileName := dl.filePath + "_" + strconv.Itoa(i)
		partFile, err := os.Open(partFileName)
		if err != nil {
			return err
		}
		defer partFile.Close()

		fileInfo, err := os.Stat(partFileName)
		buf := make([]byte, fileInfo.Size())
		partFile.Read(buf)

		if dl.checksum != "" {
			dl.hash.Write(buf)
		}

		_, err = file.Write(buf)
		if err != nil {
			return err
		}

		partFile.Close()
		err = os.Remove(partFileName)
		if err != nil {
			return err
		}
		fmt.Println("merging files ... ", i, " DONE")
	}
	fmt.Println("files saved at ", dl.filePath)
	return nil
}

func (dl *Downloader) printProgress() {
	go func() {
		dl.wg.Add(1) // ensure downInfo hits 100 percent
		defer dl.wg.Done()
		progressBarLength := 50
		started := false
		lastRun := false
		for {
			if dl.wg.Count() == 1 {
				if lastRun { // ensure downInfo hits 100 percent
					break
				}
				lastRun = true
			}
			var lines []string

			totalSpeedSum := 0.0        // Total speed sum
			totalBytesRead := int64(0)  // Total bytes read
			totalTotalBytes := int64(0) // Total total bytes (sum of all thread's total bytes)

			for i := 0; i < dl.numThreads; i++ {
				bytesRead := atomic.LoadInt64(&dl.downloadInfos[i].bytesRead)
				totalBytes := dl.downloadInfos[i].endOffset - dl.downloadInfos[i].startOffset
				speed := dl.calculateRealtimeSpeed(i, 1.15)

				progress := float64(bytesRead) / float64(totalBytes)
				progressPercent := fmt.Sprintf("%f %%", progress*100)

				totalSpeedSum += speed
				totalBytesRead += bytesRead
				totalTotalBytes += totalBytes

				// Calculate the number of completed parts (|) and remaining parts (_)
				completedParts := int(progress * float64(progressBarLength))
				remainingParts := progressBarLength - completedParts
				if remainingParts < 0 || completedParts < 0 {
					break
				}

				//
				progressBar := strings.Repeat("|", completedParts) + strings.Repeat("_", remainingParts)
				state := "Connected"
				if dl.downloadInfos[i].bytesRead == 0 {
					state = "Disconnected"
				}
				line := fmt.Sprintf("Thread %d %s %s %s / %s Speed: %s %s", i+1, progressBar, progressPercent, dl.formatSize(bytesRead), dl.formatSize(totalBytes), dl.formatSpeed(speed), state)
				lines = append(lines, line)
			}
			// Calculate total progress and total percentage
			totalProgress := float64(totalBytesRead) / float64(totalTotalBytes)
			totalProgressPercent := fmt.Sprintf("%f %%", totalProgress*100)
			totalCompletedParts := int(totalProgress * float64(progressBarLength))
			totalRemainingParts := progressBarLength - totalCompletedParts
			if totalRemainingParts < 0 || totalCompletedParts < 0 {
				break
			}
			totalProgressBar := strings.Repeat("|", totalCompletedParts) + strings.Repeat("_", totalRemainingParts)
			line := fmt.Sprintf("Total Download Speed:  %s %s %s / %s Speed: %s", totalProgressBar, totalProgressPercent, dl.formatSize(totalBytesRead), dl.formatSize(totalTotalBytes), dl.formatSpeed(totalSpeedSum))
			lines = append(lines, line)
			if started == true {
				fmt.Printf("\033[%dA", dl.numThreads-1+1)
				fmt.Print("\r")
			} else {
				started = true
			}
			fmt.Print(strings.Join(lines, "\n"))
			time.Sleep(1 * time.Second)
		}
	}()
}

func (dl *Downloader) calculateSpeed(threadNum int) float64 {
	bytesRead := atomic.LoadInt64(&dl.downloadInfos[threadNum].bytesRead)
	duration := time.Since(time.Unix(0, atomic.LoadInt64(&dl.downloadInfos[threadNum].startTime))).Seconds()
	return float64(bytesRead) / (duration * 1024)
}

func (dl *Downloader) calculateRealtimeSpeed(threadNum int, duration float64) float64 {
	bytesRead := atomic.LoadInt64(&dl.downloadInfos[threadNum].bytesRead)
	dealt := atomic.LoadInt64(&dl.downloadInfos[threadNum].dealt)
	defer atomic.SwapInt64(&dl.downloadInfos[threadNum].dealt, bytesRead)
	return float64(bytesRead-dealt) / (duration * 1024)
}

func (dl *Downloader) formatSize(size int64) string {
	if size < 1024*1024 {
		return fmt.Sprintf("%.2f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.2f MB", float64(size)/1024/1024)
}

func (dl *Downloader) formatSpeed(speed float64) string {
	if speed < 1024 {
		return fmt.Sprintf("%.2f KB/s", speed)
	}
	return fmt.Sprintf("%.2f MB/s", speed/1024)
}

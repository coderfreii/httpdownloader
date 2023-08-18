# httpdownloader
a simple downloader speeds up for http/https download by partially download 

type comand in terminal as follow to check usage
~~~cmd
httpdownloader -help 


$ Options:
$  -checksum string
$        Checksum algorithm (optional, e.g., md5, sha1)
$  -filename string
$        Specify the name of the downloaded file (optional)
$  -output string
$        Path to store the downloaded file (optional, default: current directory)
$  -threads int
$        Number of threads for concurrent download (optional, default: 10) (default 10)
$  -url string
$        URL of the file to download
~~~

checksum flag is not avaliable, the rest of it should work as exspect

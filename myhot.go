package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/howeyc/fsnotify"
)

var (
	eventTime    = make(map[string]int64)
	scheduleTime time.Time
	state        sync.Mutex
	buildTags    string
	cmd          *exec.Cmd
	started      = make(chan bool)
	currpath     string
	exit         chan bool
)

func main() {
	currpath, _ = os.Getwd()
	exit = make(chan bool)
	var paths []string
	fmt.Println(currpath)
	readAppDirectories(currpath, &paths)
	NewWatcher(paths)
	Autobuild()
	for {
		select {
		case <-exit:
			runtime.Goexit()
		}
	}
}

func readAppDirectories(dir string, paths *[]string) {
	fileInfos, err := ioutil.ReadDir(dir)
	if err != nil {
		return
	}
	useDirectory := false
	for _, fileInfo := range fileInfos {
		fmt.Printf("file ( %s ) \n", dir+"/"+fileInfo.Name())
		if strings.HasSuffix(fileInfo.Name(), "views") {
			continue
		}
		if strings.HasSuffix(fileInfo.Name(), "data") {
			continue
		}
		if fileInfo.IsDir() == true && fileInfo.Name()[0] != '.' {
			readAppDirectories(dir+"/"+fileInfo.Name(), paths)
			continue
		}
		if useDirectory == true {
			continue
		}
		if path.Ext(fileInfo.Name()) == ".go" {
			*paths = append(*paths, dir)
			useDirectory = true
		}
	}
	return
}

func NewWatcher(paths []string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("[ERRO] Fail to create new Watcher[ %s ]\n", err)
		os.Exit(2)
	}

	go func() {
		for {
			select {
			case e := <-watcher.Event:
				fmt.Printf("[ %v ]", e)
				isbuild := true
				// Skip ignored files
				if shouldIgnoreFile(e.Name) {
					continue
				}
				if !checkIfWatchExt(e.Name) {
					continue
				}
				mt := getFileModTime(e.Name)
				if t := eventTime[e.Name]; mt == t {
					fmt.Printf("[SKIP] # %s #\n", e.String())
					isbuild = false
				}
				eventTime[e.Name] = mt

				if isbuild {
					fmt.Printf("[EVEN] %s\n", e)
					go func() {
						// Wait 1s before autobuild util there is no file change.
						scheduleTime = time.Now().Add(1 * time.Second)
						for {
							time.Sleep(scheduleTime.Sub(time.Now()))
							if time.Now().After(scheduleTime) {
								break
							}
							return
						}
						Autobuild()
					}()
				}
			case err := <-watcher.Error:
				fmt.Printf("[WARN] %s\n", err.Error()) // No need to exit here
			}
		}
	}()

	fmt.Printf("[INFO] Initializing watcher...\n")
	for _, path := range paths {
		fmt.Printf("[TRAC] Directory( %s )\n", path)
		err = watcher.Watch(path)
		if err != nil {
			fmt.Printf("[ERRO] Fail to watch directory[ %s ]\n", err)
			os.Exit(2)
		}
	}
}

func Autobuild() {
	state.Lock()
	defer state.Unlock()
	fmt.Printf("[INFO] Start building...\n")
	cmdName := "go"
	var err error
	// For applications use full import path like "github.com/.../.."
	// are able to use "go install" to reduce build time.

	// icmd := exec.Command("go", "list", "./...")
	// buf := bytes.NewBuffer([]byte(""))
	// icmd.Stdout = buf
	// icmd.Env = append(os.Environ(), "GOGC=off")
	// err = icmd.Run()
	// if err == nil {
	// 	list := strings.Split(buf.String(), "\n")[1:]
	// 	for _, pkg := range list {
	// 		if len(pkg) == 0 {
	// 			continue
	// 		}
	// 		icmd = exec.Command(cmdName, "install", pkg)
	// 		icmd.Stdout = os.Stdout
	// 		icmd.Stderr = os.Stderr
	// 		icmd.Env = append(os.Environ(), "GOGC=off")
	// 		err = icmd.Run()
	// 		if err != nil {
	// 			break
	// 		}
	// 	}
	// }
	appName := "app"
	if err == nil {
		appName = path.Base(currpath)
		if runtime.GOOS == "windows" {
			appName += ".exe"
		}

		args := []string{"build"}
		args = append(args, "-o", appName)
		if buildTags != "" {
			args = append(args, "-tags", buildTags)
		}
		//args = append(args, files...)

		bcmd := exec.Command(cmdName, args...)
		bcmd.Env = append(os.Environ(), "GOGC=off")
		bcmd.Stdout = os.Stdout
		bcmd.Stderr = os.Stderr
		err = bcmd.Run()
	}

	if err != nil {
		fmt.Printf("[ERRO] ============== Build failed ===================\n")
		return
	}
	fmt.Printf("[SUCC] Build was successful\n")
	Restart(appName)
}

func shouldIgnoreFile(filename string) bool {
	for _, regex := range ignoredFilesRegExps {
		r, err := regexp.Compile(regex)
		if err != nil {
			panic("Could not compile the regex: " + regex)
		}
		if r.MatchString(filename) {
			return true
		} else {
			continue
		}
	}
	return false
}
func checkIfWatchExt(name string) bool {
	for _, s := range watchExts {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func getFileModTime(path string) int64 {
	path = strings.Replace(path, "\\", "/", -1)
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("[ERRO] Fail to open file[ %s ]\n", err)
		return time.Now().Unix()
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		fmt.Printf("[ERRO] Fail to get file information[ %s ]\n", err)
		return time.Now().Unix()
	}

	return fi.ModTime().Unix()
}

var watchExts = []string{".go"}
var ignoredFilesRegExps = []string{
	`.#(\w+).go`,
	`.(\w+).go.swp`,
	`(\w+).go~`,
	`(\w+).tmp`,
}

func Kill() {
	defer func() {
		if e := recover(); e != nil {
			fmt.Println("Kill.recover -> ", e)
		}
	}()
	if cmd != nil && cmd.Process != nil {
		err := cmd.Process.Kill()
		if err != nil {
			fmt.Println("Kill -> ", err)
		}
	}
}

func Restart(appname string) {
	fmt.Println("kill running process")
	Kill()
	go Start(appname)
}

func Start(appname string) {
	fmt.Printf("[INFO] Restarting %s ...\n", appname)
	if strings.Index(appname, "./") == -1 {
		appname = "./" + appname
	}

	cmd = exec.Command(appname)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append([]string{appname}) //conf.CmdArgs...
	cmd.Env = append(os.Environ())       //, conf.Envs...

	go cmd.Run()
	fmt.Printf("[INFO] %s is running...\n", appname)
	started <- true
}

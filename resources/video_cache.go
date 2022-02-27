package resources

import (
	"fmt"
	"github.com/gohugoio/hugo/cache/filecache"
	"github.com/gohugoio/hugo/common/herrors"
	"github.com/gohugoio/hugo/common/hexec"
	"github.com/gohugoio/hugo/helpers"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type videoCache struct {
	pathSpec  *helpers.PathSpec
	fileCache *filecache.Cache
	mu        sync.RWMutex
	store     map[string]*resourceAdapter
}

// The cache key is a lowercase path with Unix style slashes and it always starts with
// a leading slash.
func (c *videoCache) normalizeKey(key string) string {
	return "/" + c.normalizeKeyBase(key)
}

func (c *videoCache) normalizeKeyBase(key string) string {
	return strings.Trim(strings.ToLower(filepath.ToSlash(key)), "/")
}

func (c *videoCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = make(map[string]*resourceAdapter)
}

func (c *videoCache) getOrCreate(parent *videoResource, outputFormat string, ffmpegArgs string) (*resourceAdapter, error) {
	relTarget := parent.relTargetPathFromFFmpegArgs(outputFormat, ffmpegArgs)
	memKey := parent.relTargetPathForRel(relTarget.path(), false, false, false)
	memKey = c.normalizeKey(memKey)

	fileKeyPath := relTarget
	if fi := parent.getFileInfo(); fi != nil {
		fileKeyPath.dir = filepath.ToSlash(filepath.Dir(fi.Meta().Path))
	}
	fileKey := fileKeyPath.path()
	_ = fileKey

	c.mu.RLock()
	cachedVideo, found := c.store[memKey]
	c.mu.RUnlock()

	if found {
		return cachedVideo, nil
	}

	var video *videoResource
	video = &videoResource{
		baseResource: parent.baseResource.Clone().(baseResource),
	}
	rp := video.getResourcePaths()
	rp.relTargetDirFile.file = relTarget.file

	//READ
	read := func(info filecache.ItemInfo, r io.ReadSeeker) error {
		video.setSourceFilename(info.Name)
		return nil
	}

	//CREATE
	create := func(info filecache.ItemInfo, w io.WriteCloser) (err error) {
		defer w.Close()

		const binaryName = "ffmpeg"
		ex := *new(hexec.Exec)
		// TODO Security policy
		//if err := ex.Sec().CheckAllowedExec(binaryName); err != nil {
		//return err
		//

		inFile := parent.getFileInfo().Meta().Filename
		outFile, err := ioutil.TempFile("", fmt.Sprintf("*.%s", outputFormat))
		if err != nil {
			return err
		}
		defer os.Remove(outFile.Name())

		var cmdArgs []interface{}
		cmdArgs = append(cmdArgs, "-i", inFile)
		for _, a := range strings.Split(ffmpegArgs, " ") {
			cmdArgs = append(cmdArgs, a)
		}
		cmdArgs = append(cmdArgs, "-f", outputFormat)
		cmdArgs = append(cmdArgs, "-y")
		cmdArgs = append(cmdArgs, outFile.Name())
		//cmdArgs = append(cmdArgs, "pipe:1")
		//cmdArgs = append(cmdArgs, hexec.WithStdout(w))

		cmd, err := ex.New(binaryName, cmdArgs...)
		if err != nil {
			if hexec.IsNotFound(err) {
				// This may be on a CI server etc. Will fall back to pre-built assets.
				return herrors.ErrFeatureNotAvailable
			}
			return err
		}

		err = cmd.Run()
		if err != nil {
			return err
		}

		io.Copy(w, outFile)

		video.setSourceFilename(info.Name)

		//var errBuf bytes.Buffer
		//infoW := loggers.LoggerToWriterWithPrefix(logger.info(), "ffmpeg")
		//stderr := io.MultiWriter()

		return nil
	}

	_, err := c.fileCache.ReadOrCreate(fileKey, read, create)
	if err != nil {
		return nil, err
	}

	video.setSourceFs(c.fileCache.Fs)

	//STORE
	c.mu.Lock()
	videoAdapter := newResourceAdapter(parent.getSpec(), true, video)
	c.store[memKey] = videoAdapter
	c.mu.Unlock()

	return videoAdapter, nil
}

func newVideoCache(fileCache *filecache.Cache, ps *helpers.PathSpec) *videoCache {
	return &videoCache{
		fileCache: fileCache,
		pathSpec:  ps,
		store:     make(map[string]*resourceAdapter),
	}
}

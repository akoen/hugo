// Copyright 2019 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resources

import (
	"github.com/gohugoio/hugo/common/herrors"
	"github.com/gohugoio/hugo/common/hexec"
	"image"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gohugoio/hugo/resources/images"

	"github.com/gohugoio/hugo/cache/filecache"
	"github.com/gohugoio/hugo/helpers"
)

type imageCache struct {
	pathSpec *helpers.PathSpec

	fileCache *filecache.Cache

	mu    sync.RWMutex
	store map[string]*resourceAdapter
}

func (c *imageCache) deleteIfContains(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s = c.normalizeKeyBase(s)
	for k := range c.store {
		if strings.Contains(k, s) {
			delete(c.store, k)
		}
	}
}

// The cache key is a lowercase path with Unix style slashes and it always starts with
// a leading slash.
func (c *imageCache) normalizeKey(key string) string {
	return "/" + c.normalizeKeyBase(key)
}

func (c *imageCache) normalizeKeyBase(key string) string {
	return strings.Trim(strings.ToLower(filepath.ToSlash(key)), "/")
}

func (c *imageCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = make(map[string]*resourceAdapter)
}

func (c *imageCache) getOrCreate(
	parent *imageResource, conf images.ImageConfig,
	createImage func() (*imageResource, image.Image, error)) (*resourceAdapter, error) {
	relTarget := parent.relTargetPathFromConfig(conf)
	memKey := parent.relTargetPathForRel(relTarget.path(), false, false, false)
	memKey = c.normalizeKey(memKey)

	// For the file cache we want to generate and store it once if possible.
	fileKeyPath := relTarget
	if fi := parent.root.getFileInfo(); fi != nil {
		fileKeyPath.dir = filepath.ToSlash(filepath.Dir(fi.Meta().Path))
	}
	fileKey := fileKeyPath.path()

	// First check the in-memory store, then the disk.
	c.mu.RLock()
	cachedImage, found := c.store[memKey]
	c.mu.RUnlock()

	if found {
		return cachedImage, nil
	}

	var img *imageResource

	// These funcs are protected by a named lock.
	// read clones the parent to its new name and copies
	// the content to the destinations.
	read := func(info filecache.ItemInfo, r io.ReadSeeker) error {
		img = parent.clone(nil)
		rp := img.getResourcePaths()
		rp.relTargetDirFile.file = relTarget.file
		img.setSourceFilename(info.Name)

		if err := img.InitConfig(r); err != nil {
			return err
		}

		r.Seek(0, 0)

		w, err := img.openDestinationsForWriting()
		if err != nil {
			return err
		}

		if w == nil {
			// Nothing to write.
			return nil
		}

		defer w.Close()
		_, err = io.Copy(w, r)

		return err
	}

	// create creates the image and encodes it to the cache (w).
	create := func(info filecache.ItemInfo, w io.WriteCloser) (err error) {
		defer w.Close()

		var conv image.Image
		img, conv, err = createImage()
		if err != nil {
			return
		}
		rp := img.getResourcePaths()
		rp.relTargetDirFile.file = relTarget.file
		img.setSourceFilename(info.Name)

		return img.EncodeTo(conf, conv, w)
	}

	// Now look in the file cache.

	// The definition of this counter is not that we have processed that amount
	// (e.g. resized etc.), it can be fetched from file cache,
	//  but the count of processed image variations for this site.
	c.pathSpec.ProcessingStats.Incr(&c.pathSpec.ProcessingStats.ProcessedImages)

	_, err := c.fileCache.ReadOrCreate(fileKey, read, create)
	if err != nil {
		return nil, err
	}

	// The file is now stored in this cache.
	img.setSourceFs(c.fileCache.Fs)

	c.mu.Lock()
	if cachedImage, found = c.store[memKey]; found {
		c.mu.Unlock()
		return cachedImage, nil
	}

	imgAdapter := newResourceAdapter(parent.getSpec(), true, img)
	c.store[memKey] = imgAdapter
	c.mu.Unlock()

	return imgAdapter, nil
}

func (c *imageCache) videoThumb(
	video *videoResource) (*resourceAdapter, error) {
	// TODO: Should probably have its own function
	relTarget := video.relTargetPathFromFFmpegArgs("jpg", "thumb")
	memKey := video.relTargetPathForRel(relTarget.path(), false, false, false)
	memKey = c.normalizeKey(memKey)

	fileKeyPath := relTarget
	if fi := video.getFileInfo(); fi != nil {
		fileKeyPath.dir = filepath.ToSlash(filepath.Dir(fi.Meta().Path))
	}
	fileKey := fileKeyPath.path()

	c.mu.RLock()
	cachedImage, found := c.store[memKey]
	c.mu.RUnlock()

	if found {
		return cachedImage, nil
	}

	// Unsure, see resource_spec.go
	thumb := &imageResource{
		baseResource: video.baseResource.Clone().(baseResource),
	}

	rp := thumb.getResourcePaths()
	rp.relTargetDirFile.file = relTarget.file

	// TODO: Do not understand why differs from image_cache
	read := func(info filecache.ItemInfo, r io.ReadSeeker) error {
		thumb.setSourceFilename(info.Name)
		return nil
	}

	create := func(info filecache.ItemInfo, w io.WriteCloser) (err error) {
		defer w.Close()

		thumb.setSourceFilename(info.Name)

		const binaryName = "ffmpeg"
		ex := *new(hexec.Exec)
		// TODO Security policy
		//if err := ex.Sec().CheckAllowedExec(binaryName); err != nil {
		//return err
		//

		inFile := video.getFileInfo().Meta().Filename
		if err != nil {
			return err
		}

		var cmdArgs []interface{}
		cmdArgs = append(cmdArgs, "-i", inFile)
		cmdArgs = append(cmdArgs, "-vf", "thumbnail", "-frames:v", "1")
		cmdArgs = append(cmdArgs, "-f", "image2")
		cmdArgs = append(cmdArgs, "-y")
		cmdArgs = append(cmdArgs, "pipe:1")
		cmdArgs = append(cmdArgs, hexec.WithStdout(w))

		cmd, err := ex.New(binaryName, cmdArgs...)
		if err != nil {
			if hexec.IsNotFound(err) {
				return herrors.ErrFeatureNotAvailable
			}
			return err
		}

		err = cmd.Run()
		if err != nil {
			return err
		}

		return nil
	}

	_, err := c.fileCache.ReadOrCreate(fileKey, read, create)
	if err != nil {
		return nil, err
	}

	thumb.setSourceFs(c.fileCache.Fs)
	imgFormat, _ := images.ImageFormatFromExt(".jpg")
	thumb.Image = images.NewImage(imgFormat, video.getSpec().imaging, nil, video.baseResource)
	thumb.root = thumb

	//STORE
	c.mu.Lock()
	thumbAdapter := newResourceAdapter(thumb.getSpec(), true, thumb)
	c.store[memKey] = thumbAdapter
	c.mu.Unlock()

	return thumbAdapter, nil
}

func newImageCache(fileCache *filecache.Cache, ps *helpers.PathSpec) *imageCache {
	return &imageCache{fileCache: fileCache, pathSpec: ps, store: make(map[string]*resourceAdapter)}
}

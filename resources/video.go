package resources

import (
	"fmt"
	"github.com/gohugoio/hugo/common/paths"
	"github.com/gohugoio/hugo/helpers"
	"github.com/gohugoio/hugo/resources/resource"
)

var (
	_ resource.Video = (*videoResource)(nil)
)

type videoResource struct {
	//*videos.Video

	//metaInit    sync.Once
	//metaInitErr error
	//meta        *imageMeta
	baseResource
}

func (v *videoResource) relTargetPathFromFFmpegArgs(outputFormat string, ffmpegArgs string) dirFile {
	p1, _ := paths.FileAndExt(v.getResourcePaths().relTargetDirFile.file)

	key := v.TranscodeKey(ffmpegArgs)

	return dirFile{
		dir:  v.getResourcePaths().relTargetDirFile.dir,
		file: fmt.Sprintf("%s_%s.%s", p1, key, outputFormat),
	}
}

func (v *videoResource) Transcode(format string, spec string) (resource.Video, error) {
	return v.getSpec().videoCache.getOrCreate(v, format, spec)
}

func (v *videoResource) TranscodeKey(ffmpegArgs string) string {
	p1, p2 := paths.FileAndExt(v.getResourcePaths().relTargetDirFile.file)
	return helpers.MD5String(p1 + p2 + ffmpegArgs)
}

func (v *videoResource) Thumbnail() (resource.Image, error) {
	return v.baseResource.getSpec().imageCache.videoThumb(v)
}

package videos

import (
	"github.com/u2takey/ffmpeg-go"
)

type Video struct {
	Format Format
	// Proc   *ImageProcessor
	// Spec   Spec
}

func test() {
	ffmpeg_go.Input("test")
}

type Format int

const (
	MP4 Format = iota + 1
	MOV
)

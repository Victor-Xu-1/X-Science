package server

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"

	_ "golang.org/x/image/webp"
)

const maxVisualArtifactPixels = 32 * 1024 * 1024

// A correct signature and hash prove byte identity, not a renderable image.
// Bound compressed bytes and decoded dimensions before allocating pixel data.
func validateVisualArtifactImage(content []byte) error {
	if len(content) == 0 || len(content) > agentWorkspaceVisualMaxBytes {
		return errors.New("visual image exceeds the 25 MiB byte limit")
	}
	if !agentWorkspaceImageMIME(http.DetectContentType(content)) {
		return errors.New("visual content is not a supported raster image")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxVisualArtifactPixels/config.Height {
		return errors.New("visual image exceeds the 32 megapixel decoded limit")
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return err
	}
	if decodedFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return errors.New("visual image dimensions or format changed during decoding")
	}
	return nil
}

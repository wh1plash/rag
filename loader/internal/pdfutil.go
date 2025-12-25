package internal

import (
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// RemoveHeaderFooterCrop обрезает верхние и нижние колонтитулы PDF файла.
// top и bottom задаются в пунктах (1 pt = 1/72 inch).
func RemoveHeaderFooterCrop(inputPath, outputPath string, top, bottom float64) error {
	conf := api.LoadConfiguration()

	pages := []string{"1-"}

	// + виртуальный отступ (≈ 2 строки текста)
	const padding = 20.0

	cropStr := fmt.Sprintf(
		"%.2f 0 %.2f 0",
		top,
		bottom,
	)

	box, err := model.ParseBox(cropStr, types.POINTS)
	if err != nil {
		return fmt.Errorf("failed to parse crop box: %w", err)
	}

	if err := api.CropFile(inputPath, outputPath, pages, box, conf); err != nil {
		return fmt.Errorf("failed to crop PDF: %w", err)
	}

	return nil
}

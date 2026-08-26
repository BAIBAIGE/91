package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/videoname"
)

// maintainLocalUploadFileNames migrates legacy local uploads from random
// physical names to the user-visible title. It is intentionally best-effort:
// one invalid or missing legacy file must not block the nightly maintenance
// pipeline for every other video.
func (a *App) maintainLocalUploadFileNames(ctx context.Context) {
	if a == nil || a.cat == nil {
		return
	}
	root := a.localUploadDir()
	videos, err := a.cat.ListVideosByDrive(ctx, localupload.DriveID)
	if err != nil {
		log.Printf("[local-upload-maintenance] list videos: %v", err)
		return
	}
	renamed, skipped := 0, 0
	for _, v := range videos {
		if err := ctx.Err(); err != nil {
			return
		}
		if v == nil || filepath.Base(v.FileID) != v.FileID {
			skipped++
			continue
		}
		title := strings.TrimSpace(v.Title)
		if title == "" {
			title = videoname.TitleFromFileName(v.FileName)
		}
		ext := filepath.Ext(v.FileID)
		if ext == "" && strings.TrimSpace(v.Ext) != "" {
			ext = "." + strings.TrimPrefix(strings.TrimSpace(v.Ext), ".")
		}
		if err := videoname.ValidateUploadTitle(title, ext); err != nil {
			log.Printf("[local-upload-maintenance] skip id=%s invalid title=%q: %v", v.ID, title, err)
			skipped++
			continue
		}

		newName := videoname.UploadFileName(title, ext, v.FileID, false)
		oldPath := filepath.Join(root, v.FileID)
		newPath := filepath.Join(root, newName)
		if newName != v.FileID {
			if _, err := os.Stat(oldPath); err != nil {
				log.Printf("[local-upload-maintenance] skip id=%s source=%q: %v", v.ID, v.FileID, err)
				skipped++
				continue
			}
			if _, err := os.Stat(newPath); err == nil {
				uploadID := strings.TrimSuffix(v.FileID, filepath.Ext(v.FileID))
				newName = videoname.UploadFileName(title, ext, uploadID, true)
				newPath = filepath.Join(root, newName)
				if _, err := os.Stat(newPath); err == nil || !os.IsNotExist(err) {
					log.Printf("[local-upload-maintenance] skip id=%s collision=%q", v.ID, newName)
					skipped++
					continue
				}
			} else if !os.IsNotExist(err) {
				log.Printf("[local-upload-maintenance] skip id=%s destination=%q: %v", v.ID, newName, err)
				skipped++
				continue
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				log.Printf("[local-upload-maintenance] rename id=%s %q -> %q: %v", v.ID, v.FileID, newName, err)
				skipped++
				continue
			}
		}

		finalTitle := videoname.TitleFromFileName(newName)
		if err := a.cat.UpdateVideoFileIdentity(ctx, v.ID, newName, newName, finalTitle); err != nil {
			if newName != v.FileID {
				if rollbackErr := os.Rename(newPath, oldPath); rollbackErr != nil {
					log.Printf("[local-upload-maintenance] rollback id=%s: %v", v.ID, rollbackErr)
				}
			}
			log.Printf("[local-upload-maintenance] catalog id=%s: %v", v.ID, err)
			skipped++
			continue
		}
		if newName != v.FileID || v.FileName != newName || v.Title != finalTitle {
			renamed++
		}
	}
	log.Printf("[local-upload-maintenance] videos=%d updated=%d skipped=%d", len(videos), renamed, skipped)
}

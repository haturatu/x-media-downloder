package main

const (
	taskTypeDownload        = "xmd:download_tweet_media"
	taskTypeAutotagAll      = "xmd:autotag_all"
	taskTypeAutotagUntagged = "xmd:autotag_untagged"
	taskTypeReconcileDB     = "xmd:reconcile_db"
	taskTypeDeleteUser      = "xmd:delete_user"
	taskTypeDeleteImage     = "xmd:delete_image"
	taskTypeDeleteImages    = "xmd:delete_images"
	taskTypeRetagImage      = "xmd:retag_image"
	taskTypeRetagImages     = "xmd:retag_images"

	taskListKey              = "xmd:download_task_ids"
	taskURLHashKey           = "xmd:download_task_urls"
	autotagLastTask          = "xmd:autotag:last_task_id"
	autotagDownloadStatusKey = "xmd:autotag:download:status"
	retagLastTask            = "xmd:retag:last_task_id"
	taskMetaPrefix           = "xmd:task-meta-"
	maxTrackedTasks          = 200
)

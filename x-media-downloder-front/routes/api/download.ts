// x-media-downloder-front/routes/api/download.ts

import { FreshContext } from "$fresh/server.ts";
import { connect, Redis } from "redis";
import { createCeleryMessage } from "../../utils/celery.ts";

let redis: Redis | null = null;
const TASK_LIST_KEY = "xmd:download_task_ids";
const TASK_URL_HASH_KEY = "xmd:download_task_urls";
const RESULT_PREFIX = "celery-task-meta-";
const MAX_TRACKED_TASKS = 200;

async function getRedisConnection(): Promise<Redis> {
  if (redis) {
    return redis;
  }
  // The hostname 'redis' is from the docker-compose service name.
  redis = await connect({
    hostname: "redis",
    port: 6379,
  });
  return redis;
}

interface DownloadRequest {
  urls: string[];
}

type DownloadTaskState = "PENDING" | "PROGRESS" | "SUCCESS" | "FAILURE";

interface DownloadTaskStatus {
  task_id: string;
  url: string | null;
  state: DownloadTaskState;
  message: string;
  current?: number;
  total?: number;
  downloaded_count?: number;
  skipped_count?: number;
}

async function handlePost(_req: Request): Promise<Response> {
  try {
    const body: DownloadRequest = await _req.json();
    const urls = body.urls;
    if (!urls || !Array.isArray(urls)) {
      return new Response(
        JSON.stringify({ success: false, message: "URL list is required" }),
        { status: 400 },
      );
    }

    const redisConn = await getRedisConnection();
    let count = 0;
    const queuedTasks: Array<{ task_id: string; url: string }> = [];

    for (const url of urls) {
      if (
        typeof url === "string" &&
        (url.includes("x.com") || url.includes("twitter.com")) &&
        url.includes("/status/")
      ) {
        const [message, taskId] = createCeleryMessage(
          "tasks.download_tweet_media",
          [url],
        );

        await redisConn.lpush("celery", message);
        await redisConn.rpush(TASK_LIST_KEY, taskId);
        await redisConn.hset(TASK_URL_HASH_KEY, taskId, url);
        count++;
        queuedTasks.push({ task_id: taskId, url });
      }
    }

    // Keep tracked task IDs bounded to avoid unbounded memory growth in Redis.
    await redisConn.ltrim(TASK_LIST_KEY, -MAX_TRACKED_TASKS, -1);

    return new Response(
      JSON.stringify({
        success: true,
        message: `${count} download tasks have been queued.`,
        queued_tasks: queuedTasks,
      }),
      {
        headers: { "Content-Type": "application/json" },
      },
    );
  } catch (error) {
    console.error("Error queueing download task:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
}

async function resolveTaskStatus(
  redisConn: Redis,
  taskId: string,
): Promise<DownloadTaskStatus> {
  const resultJson = await redisConn.get(`${RESULT_PREFIX}${taskId}`);
  const url = await redisConn.hget(TASK_URL_HASH_KEY, taskId);

  if (!resultJson) {
    return {
      task_id: taskId,
      url,
      state: "PENDING",
      message: "Queued or running",
    };
  }

  try {
    const result = JSON.parse(resultJson as string);
    const state = (result.status || "PENDING") as DownloadTaskState;
    const payload = result.result;

    if (state === "PROGRESS" && typeof payload === "object" && payload) {
      const typedPayload = payload as {
        current?: number;
        total?: number;
        status?: string;
      };
      return {
        task_id: taskId,
        url,
        state,
        message: typedPayload.status || "Running",
        current: typedPayload.current,
        total: typedPayload.total,
      };
    }

    if (state === "SUCCESS" && typeof payload === "object" && payload) {
      const typedPayload = payload as {
        message?: string;
        downloaded_count?: number;
        skipped_count?: number;
      };
      return {
        task_id: taskId,
        url,
        state,
        message: typedPayload.message || "Completed",
        downloaded_count: typedPayload.downloaded_count,
        skipped_count: typedPayload.skipped_count,
      };
    }

    if (state === "FAILURE") {
      return {
        task_id: taskId,
        url,
        state,
        message: String(payload || "Task failed"),
      };
    }

    return {
      task_id: taskId,
      url,
      state,
      message: "Running",
    };
  } catch {
    return {
      task_id: taskId,
      url,
      state: "PENDING",
      message: "Queued or running",
    };
  }
}

async function handleGet(req: Request): Promise<Response> {
  try {
    const redisConn = await getRedisConnection();
    const queueDepth = await redisConn.llen("celery");
    const url = new URL(req.url);
    const requestedIds = url.searchParams.get("ids");
    let taskIds: string[] = [];

    if (requestedIds) {
      taskIds = requestedIds.split(",").map((id) => id.trim()).filter(Boolean);
    } else {
      taskIds = await redisConn.lrange(TASK_LIST_KEY, -30, -1);
    }

    taskIds = Array.from(new Set(taskIds)).reverse();

    const items: DownloadTaskStatus[] = [];
    for (const taskId of taskIds) {
      items.push(await resolveTaskStatus(redisConn, taskId));
    }

    const summary = {
      total: items.length,
      pending: items.filter((item) =>
        item.state === "PENDING" || item.state === "PROGRESS"
      ).length,
      success: items.filter((item) =>
        item.state === "SUCCESS"
      ).length,
      failure: items.filter((item) => item.state === "FAILURE").length,
    };

    return new Response(
      JSON.stringify({
        queue_depth: queueDepth,
        summary,
        items,
      }),
      {
        headers: { "Content-Type": "application/json" },
      },
    );
  } catch (error) {
    console.error("Error fetching download task status:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
}

export const handler = async (
  _req: Request,
  _ctx: FreshContext,
): Promise<Response> => {
  if (_req.method === "POST") {
    return await handlePost(_req);
  }
  if (_req.method === "GET") {
    return await handleGet(_req);
  }
  return new Response(null, { status: 405 });
};

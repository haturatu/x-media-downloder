// x-media-downloder-front/routes/api/download.ts

import { FreshContext } from "$fresh/server.ts";
import { connect, Redis } from "redis";

let redis: Redis | null = null;

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

import { createCeleryMessage } from "../../utils/celery.ts";

interface DownloadRequest {
  urls: string[];
}

export const handler = async (_req: Request, _ctx: FreshContext): Promise<Response> => {
  if (_req.method !== 'POST') {
    return new Response(null, { status: 405 });
  }

  try {
    const body: DownloadRequest = await _req.json();
    const urls = body.urls;
    if (!urls || !Array.isArray(urls)) {
      return new Response(JSON.stringify({ success: false, message: "URL list is required" }), { status: 400 });
    }
    
    const redisConn = await getRedisConnection();
    let count = 0;

    for (const url of urls) {
      if (typeof url === 'string' && (url.includes('x.com') || url.includes('twitter.com')) && url.includes('/status/')) {
        
        const [message, _taskId] = createCeleryMessage('tasks.download_tweet_media', [url]);

        // LPUSH to the default celery queue
        await redisConn.lpush("celery", message);
        count++;
      }
    }
    
    return new Response(JSON.stringify({ success: true, message: `${count} download tasks have been queued.` }), {
      headers: { "Content-Type": "application/json" },
    });

  } catch (error) {
    console.error("Error queueing download task:", error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), {
      status: 500,
      headers: { "Content-Type": "application/json" },
    });
  }
};

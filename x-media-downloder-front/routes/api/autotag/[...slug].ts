// x-media-downloder-front/routes/api/autotag/[...slug].ts

import { FreshContext } from "$fresh/server.ts";
import { connect, Redis } from "redis";
import { createCeleryMessage } from "../../../utils/celery.ts";

let redis: Redis | null = null;
// In-memory store for the last task ID, similar to the Python implementation.
const TASK_STORE = {
  autotag_task_id: null as string | null,
};

async function getRedisConnection(): Promise<Redis> {
  if (redis) return redis;
  redis = await connect({ hostname: "redis", port: 6379 });
  return redis;
}

async function handlePost(slug: string): Promise<Response> {
  let taskName = '';
  let successMessage = '';

  if (slug === 'reload') {
    taskName = 'app._autotag_all_task'; // Assumes task name from module
    successMessage = 'Started force re-tagging for ALL images in the background.';
  } else if (slug === 'untagged') {
    taskName = 'app._autotag_untagged_task'; // Assumes task name from module
    successMessage = 'Autotagging for untagged images started in the background.';
  } else {
    return new Response(null, { status: 404 });
  }

  try {
    const redisConn = await getRedisConnection();
    const [celeryMessage, taskId] = createCeleryMessage(taskName, []);
    
    // Store the task ID for the status endpoint
    TASK_STORE.autotag_task_id = taskId;

    await redisConn.lpush("celery", celeryMessage);

    return new Response(JSON.stringify({ success: true, message: successMessage, task_id: taskId }), {
      headers: { "Content-Type": "application/json" },
    });
  } catch (error) {
    console.error(`Error queueing ${slug} task:`, error);
    return new Response(JSON.stringify({ error: "Internal Server Error" }), { status: 500 });
  }
}


async function handleGet(slug: string): Promise<Response> {
    if (slug !== 'status') {
        return new Response(null, { status: 404 });
    }

    const taskId = TASK_STORE.autotag_task_id;
    if (!taskId) {
        return new Response(JSON.stringify({ state: 'NOT_FOUND', status: 'No autotagging task has been run yet.' }), {
            headers: { "Content-Type": "application/json" }
        });
    }

    try {
        const redisConn = await getRedisConnection();
        // Celery stores results with a specific key format.
        const resultKey = `celery-task-meta-${taskId}`;
        const resultJson = await redisConn.get(resultKey);

        if (!resultJson) {
            // Task result might not be available yet, or it expired.
            // Celery might also not have created it yet.
            return new Response(JSON.stringify({ state: 'PENDING', status: 'Task is pending...' }), {
                headers: { "Content-Type": "application/json" }
            });
        }
        
        const result = JSON.parse(resultJson as string);

        // Translate Celery result format to the format expected by the frontend.
        const response = {
            state: result.status,
            status: result.result?.status || result.result || 'Processing...',
            current: result.result?.current,
            total: result.result?.total
        };

        return new Response(JSON.stringify(response), {
            headers: { "Content-Type": "application/json" }
        });

    } catch (error) {
        console.error(`Error fetching status for task ${taskId}:`, error);
        return new Response(JSON.stringify({ error: "Internal Server Error" }), { status: 500 });
    }
}


export const handler = async (_req: Request, ctx: FreshContext): Promise<Response> => {
  // The slug is the part of the path after /api/autotag/
  const slug = ctx.params.slug;

  if (_req.method === 'POST') {
    return await handlePost(slug);
  } else if (_req.method === 'GET') {
    return await handleGet(slug);
  } else {
    return new Response(null, { status: 405 });
  }
};

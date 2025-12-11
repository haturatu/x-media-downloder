// x-media-downloder-front/utils/celery.ts

import { encodeBase64 } from "$std/encoding/base64.ts";

/**
 * Creates a message object that is compliant with the Celery message protocol v2,
 * which is expected by the Python worker.
 * @param taskName The name of the task to execute (e.g., 'tasks.download_tweet_media')
 * @param args The positional arguments for the task.
 * @returns A JSON string representing the full Celery message.
 */
export function createCeleryMessage(taskName: string, args: unknown[]): [string, string] {
  const taskId = crypto.randomUUID();
  
  const taskBody = {
    task: taskName,
    id: taskId,
    args: args,
    kwargs: {},
    // Add other fields Celery might need inside the body
    retries: 0,
    eta: null,
    expires: null,
    utc: true,
    callbacks: null,
    errbacks: null,
    chain: null,
    chord: null,
  };

  const encodedBody = encodeBase64(JSON.stringify(taskBody));

  const message = {
    body: encodedBody,
    "content-encoding": "utf-8",
    "content-type": "application/json",
    headers: {},
    properties: {
      body_encoding: "base64",
      correlation_id: taskId,
      delivery_info: {
        exchange: "",
        routing_key: "celery", // Default queue name
      },
      delivery_mode: 2, // Persistent
      delivery_tag: crypto.randomUUID(),
      priority: 0,
      reply_to: crypto.randomUUID(),
    },
  };

  return [JSON.stringify(message), taskId];
}

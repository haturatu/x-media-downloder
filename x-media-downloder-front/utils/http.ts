export interface PageProps<T = Record<string, never>> {
  data: T;
}

export interface RequestContext<T = unknown, P = Record<string, string>> {
  params: P;
  render(data: T): Response;
}

export type Handler<T = unknown> = (
  request: Request,
  context: RequestContext<T>,
) => Response | Promise<Response>;

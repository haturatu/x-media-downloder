FROM denoland/deno:latest

WORKDIR /app

# Cache dependencies
# Copy deps file and lock file to leverage Docker cache
COPY deno.json .
COPY fresh.gen.ts .

COPY . .

# Use `deno cache` to download and cache dependencies
# This step is critical for faster builds
# RUN deno cache --lock=deno.lock routes/_app.tsx main.ts

# Generate the manifest and build the project
# This creates the _fresh directory
RUN deno task build

# Expose the port Fresh runs on
EXPOSE 8000

# Command to run the Fresh application
CMD ["deno", "run", "-A", "--unstable", "main.ts"]

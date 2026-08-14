# ---------- build stage ----------
FROM node:22-alpine AS builder
WORKDIR /app



# Corepack ships with recent Node; it activates Yarn v1 by default.
RUN corepack enable

# Copy manifest *and* lock file
COPY package.json yarn.lock ./

# Install prod + dev deps reproducibly
RUN yarn install

# Copy the rest of the source and build
COPY . .
RUN yarn build          # or `yarn run build`, whatever your script name is

# ---------- runtime stage ----------
FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production

# Only copy the built output
COPY --from=builder /app/.output .output
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]

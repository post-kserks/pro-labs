# ─── Stage 1: Builder ────────────────────────────────────────────
FROM node:20-alpine AS builder

WORKDIR /app

COPY package.json ./
RUN npm install --no-audit --no-fund

COPY . .

ARG VITE_API_URL=http://localhost:4000
ENV VITE_API_URL=$VITE_API_URL

RUN npm run build

# ─── Stage 2: Nginx ──────────────────────────────────────────────
FROM nginx:1.27-alpine

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]

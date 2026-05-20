/** @type {import('next').NextConfig} */
// `output: 'standalone'` is only useful for self-hosted Node runtimes
// (e.g. our Docker image). Vercel ships its own serverless runtime and
// must NOT see `standalone`, otherwise the platform middleware can't
// find the entry and serves a 404 at the edge.
const nextConfig = {
  ...(process.env.BUILD_STANDALONE === '1' ? { output: 'standalone' } : {})
};

export default nextConfig;

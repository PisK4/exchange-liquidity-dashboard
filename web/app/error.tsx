'use client';

export default function Error({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <section className="panel span-12">
      <h2>API error</h2>
      <p className="error">{error.message}</p>
      <button className="control" onClick={reset}>Retry</button>
    </section>
  );
}

// RouteError: what a page shows when it fails to draw, instead of the router's developer screen.
// The details stay one click away for bug reports.
import { isRouteErrorResponse, Link, useRouteError } from "react-router-dom";

export function RouteError() {
  const err = useRouteError();
  const notFound = isRouteErrorResponse(err) && err.status === 404;
  const detail = isRouteErrorResponse(err) ? `${err.status} ${err.statusText}` : err instanceof Error ? `${err.message}\n\n${err.stack ?? ""}` : String(err);
  return (
    <div className="page" style={{ maxWidth: 640, margin: "48px auto" }}>
      <div className="panel"><div className="panel-body">
        <h2 style={{ marginTop: 0 }}>{notFound ? "This page doesn't exist" : "Something went wrong on this page"}</h2>
        <p className="muted">{notFound ? "The address may be mistyped, or the item was deleted." : "Your saved work is safe. Reload the page, or go back and try again. If it keeps happening, send us the details below."}</p>
        <div className="row" style={{ gap: 8 }}>
          <button type="button" className="btn primary" onClick={() => window.location.reload()}>Reload</button>
          <button type="button" className="btn" onClick={() => window.history.back()}>Go back</button>
          <Link className="btn ghost" to="/">Overview</Link>
        </div>
        {!notFound && (
          <details style={{ marginTop: 16 }}>
            <summary className="small muted">Details for a bug report</summary>
            <pre className="xs" style={{ whiteSpace: "pre-wrap", overflow: "auto", maxHeight: 240 }}>{detail}</pre>
          </details>
        )}
      </div></div>
    </div>
  );
}

import { StrictMode, useCallback, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ArrowDownToLine,
  ArrowRight,
  Box,
  Check,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  Copy,
  Layers3,
  LoaderCircle,
  Package,
  Plus,
  RefreshCw,
  Search,
  Server,
  Settings2,
  HardDrive,
  LogOut,
  ShieldCheck,
  Terminal,
  Trash2,
} from "lucide-react";
import { Modal } from "./Modal";
import { api } from "./api";
import { Root } from "./Auth";
import type { Session } from "./Auth";
import { StoragePanel } from "./Storage";
import "./style.css";

type Tag = {
  name: string;
  digest: string;
  size: number;
  created?: string;
  platforms: string[];
  mediaType?: string;
  error?: string;
};
type Status = { connected: boolean; registry: string; authEnabled: boolean };

function bytes(value: number) {
  if (!value) return "—";
  const unit = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4);
  return `${(value / 1024 ** unit).toFixed(1)} ${["B", "KB", "MB", "GB", "TB"][unit]}`;
}

function date(value?: string) {
  if (!value || Number.isNaN(Date.parse(value))) return "Unavailable";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(new Date(value));
}

function App({
  session,
  onLogout,
}: {
  session: Session;
  onLogout: () => void;
}) {
  const [view, setView] = useState<"repositories" | "storage">("repositories");
  const [status, setStatus] = useState<Status | null>(null);
  const [repos, setRepos] = useState<string[]>([]);
  const [selected, setSelected] = useState("");
  const [tags, setTags] = useState<Tag[]>([]);
  const [query, setQuery] = useState("");
  const [tagQuery, setTagQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [tagsLoading, setTagsLoading] = useState(false);
  const [error, setError] = useState("");
  const [tagError, setTagError] = useState("");
  const [modal, setModal] = useState<"push" | "connection" | "help" | null>(
    null,
  );
  const [deleting, setDeleting] = useState<Tag | null>(null);
  const [busy, setBusy] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const [toast, setToast] = useState("");
  const [image, setImage] = useState("my-app");
  const [imageTag, setImageTag] = useState("latest");
  const [reload, setReload] = useState(0);
  const registry =
    status?.registry ||
    (import.meta.env.DEV ? "localhost:5050" : window.location.host);
  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [health, data] = await Promise.all([
        api<Status>("status"),
        api<{ repositories: string[] }>("repositories"),
      ]);
      setStatus(health);
      setRepos(data.repositories);
      setSelected((current) =>
        data.repositories.includes(current)
          ? current
          : data.repositories[0] || "",
      );
      setReload((value) => value + 1);
    } catch (err) {
      setError((err as Error).message);
      setStatus(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      if (
        event.key === "/" &&
        !document.querySelector("dialog[open]") &&
        !(event.target instanceof HTMLInputElement) &&
        !(event.target instanceof HTMLTextAreaElement)
      ) {
        event.preventDefault();
        document.getElementById("repo-search")?.focus();
      }
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setTags([]);
    setTagError("");
    setTagQuery("");
    if (!selected) {
      setTagsLoading(false);
      return;
    }
    setTagsLoading(true);
    api<{ tags: Tag[] }>(`tags?repository=${encodeURIComponent(selected)}`, {
      signal: controller.signal,
    })
      .then((data) => setTags(data.tags))
      .catch((err) => {
        if (!controller.signal.aborted) setTagError(err.message);
      })
      .finally(() => {
        if (!controller.signal.aborted) setTagsLoading(false);
      });
    return () => controller.abort();
  }, [selected, reload]);
  useEffect(() => {
    if (toast) {
      const timer = setTimeout(() => setToast(""), 3000);
      return () => clearTimeout(timer);
    }
  }, [toast]);

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setToast("Copied to clipboard");
    } catch {
      setToast("Clipboard unavailable. Select and copy the command manually.");
    }
  }
  async function remove() {
    if (!deleting) return;
    setBusy(true);
    setDeleteError("");
    try {
      await api(
        `manifests?repository=${encodeURIComponent(selected)}&digest=${encodeURIComponent(deleting.digest)}`,
        { method: "DELETE" },
      );
      setDeleting(null);
      setToast("Manifest deleted");
      await refresh();
    } catch (err) {
      setDeleteError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const visibleRepos = repos.filter((repo) =>
    repo.toLowerCase().includes(query.toLowerCase()),
  );
  const visibleTags = tags.filter((tag) =>
    `${tag.name} ${tag.digest} ${tag.platforms.join(" ")}`
      .toLowerCase()
      .includes(tagQuery.toLowerCase()),
  );
  const manifestCount = new Set(
    tags.filter((tag) => tag.digest).map((tag) => tag.digest),
  ).size;
  const validImage =
    /^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:\/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$/.test(
      image,
    );
  const validTag = /^[\w][\w.-]{0,127}$/.test(imageTag);
  const imageRef = `${image}:${imageTag}`;
  const target = `${registry}/${imageRef}`;
  const command = (text: string) => (
    <div className="command">
      <code>{text}</code>
      <button
        className="icon-button"
        onClick={() => void copy(text)}
        aria-label={`Copy ${text}`}
      >
        <Copy size={15} />
      </button>
    </div>
  );

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="Magistry home">
          <span className="brand-mark">
            <Box size={25} strokeWidth={1.8} />
          </span>
          magistry<span className="brand-period">.</span>
        </a>
        <div className="workspace">
          <span className="workspace-icon">
            <Layers3 size={17} />
          </span>
          <div>
            <strong>My registry</strong>
            <small>Self-hosted workspace</small>
          </div>
          <ChevronDown size={14} />
        </div>
        <div className="nav-label">WORKSPACE</div>
        <nav>
          <button
            className={`nav-item ${view === "repositories" ? "active" : ""}`}
            onClick={() => {
              setModal(null);
              setView("repositories");
              document.getElementById("repo-search")?.focus();
            }}
          >
            <Package size={18} />
            Repositories<span>{repos.length}</span>
          </button>
          <button
            className={`nav-item ${view === "storage" ? "active" : ""}`}
            onClick={() => setView("storage")}
          >
            <HardDrive size={18} />
            Storage
          </button>
          <button className="nav-item" onClick={() => setModal("connection")}>
            <Settings2 size={18} />
            Connection
          </button>
        </nav>
        <div className="sidebar-bottom">
          <button
            className="nav-item"
            onClick={onLogout}
            title={`Sign out ${session.username}`}
          >
            <LogOut size={18} />
            Sign out<span className="signed-in-user">{session.username}</span>
          </button>
          <div className="storage-note">
            <ShieldCheck size={19} />
            <strong>Your images. Your server.</strong>
            <p>Stored in a persistent Docker volume, under your control.</p>
          </div>
          <button className="nav-item" onClick={() => setModal("help")}>
            <CircleHelp size={18} />
            Quick reference
            <ArrowRight size={15} />
          </button>
          <div className="version">
            <span className={`status-dot ${status ? "" : "offline"}`} />
            {status
              ? "Registry connected"
              : loading
                ? "Connecting…"
                : "Registry offline"}
            <span>v0.1.0</span>
          </div>
        </div>
      </aside>

      <main>
        <header className="topbar">
          <div>
            Workspace
            <ChevronRight size={14} />
            <strong>{view === "storage" ? "Storage" : "Repositories"}</strong>
          </div>
          <span className="local-label">
            <Server size={14} />
            Self-hosted
          </span>
        </header>
        {view === "storage" ? (
          <StoragePanel />
        ) : (
          <div className="content">
            <div className="page-heading">
              <div>
                <div className="eyebrow">CONTAINER REGISTRY</div>
                <h1>
                  Repositories<span>.</span>
                </h1>
                <p>Browse, pull, and manage your container images.</p>
              </div>
              <button className="primary" onClick={() => setModal("push")}>
                <Plus size={17} />
                Push an image
              </button>
            </div>
            <div className="registry-bar">
              <div>
                <span className={`status-dot ${status ? "" : "offline"}`} />
                <code>{registry}</code>
                <button
                  className="icon-button"
                  onClick={() => void copy(registry)}
                  aria-label="Copy registry address"
                >
                  <Copy size={14} />
                </button>
              </div>
              <span>
                {status?.authEnabled ? (
                  <>
                    <ShieldCheck size={14} />
                    Authentication enabled
                  </>
                ) : (
                  <>
                    <Server size={14} />
                    Open access
                  </>
                )}
              </span>
            </div>
            {error && (
              <div role="alert" className="alert">
                <strong>Registry unavailable</strong>
                <span>{error}</span>
                <button onClick={() => void refresh()}>Try again</button>
              </div>
            )}
            <div className="browser-toolbar">
              <div className="section-title">
                Image library
                <span>
                  {repos.length}{" "}
                  {repos.length === 1 ? "repository" : "repositories"}
                </span>
              </div>
              <button
                className="text-button"
                onClick={() => void refresh()}
                disabled={loading || tagsLoading}
              >
                <RefreshCw size={15} className={loading ? "spin" : ""} />
                Refresh
              </button>
            </div>
            <section className="registry-browser" aria-label="Image library">
              <div className="repository-pane">
                <label className="search">
                  <Search size={16} />
                  <input
                    id="repo-search"
                    aria-label="Search repositories"
                    placeholder="Find a repository…"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                  />
                  <kbd>/</kbd>
                </label>
                <div className="list-caption">REPOSITORY NAME</div>
                <div className="repository-list">
                  {loading && !repos.length ? (
                    <div className="list-message">
                      <LoaderCircle size={20} className="spin" />
                      Loading repositories…
                    </div>
                  ) : (
                    visibleRepos.map((repo) => (
                      <button
                        key={repo}
                        className={`repo-item ${selected === repo ? "selected" : ""}`}
                        onClick={() => setSelected(repo)}
                      >
                        <span className="repo-icon">
                          <Box size={18} />
                        </span>
                        <span title={repo}>
                          {repo.includes("/") && (
                            <small>
                              {repo.slice(0, repo.lastIndexOf("/"))}/
                            </small>
                          )}
                          <strong>{repo.split("/").at(-1)}</strong>
                        </span>
                        <ChevronRight size={15} />
                      </button>
                    ))
                  )}
                  {!loading && !visibleRepos.length && (
                    <div className="list-message">
                      {query
                        ? "No matching repositories."
                        : "Your repositories will appear here."}
                    </div>
                  )}
                </div>
                <div className="pane-footer">
                  <span className="status-dot" />
                  Docker Registry API v2
                </div>
              </div>
              <div className="detail-pane">
                {selected ? (
                  <>
                    <div className="repo-heading">
                      <span className="detail-icon">
                        <Box size={24} />
                      </span>
                      <div>
                        <span className="detail-caption">REPOSITORY</span>
                        <h2>{selected}</h2>
                      </div>
                      <button
                        className="icon-button"
                        onClick={() => void copy(`${registry}/${selected}`)}
                        aria-label="Copy repository address"
                      >
                        <Copy size={16} />
                      </button>
                    </div>
                    <div className="repo-facts">
                      <span>
                        <Layers3 size={15} />
                        <strong>{tagsLoading ? "—" : tags.length}</strong> tags
                      </span>
                      <span>
                        <Package size={15} />
                        <strong>
                          {tagsLoading ? "—" : manifestCount}
                        </strong>{" "}
                        manifests
                      </span>
                    </div>
                    <div className="pull-bar">
                      <Terminal size={15} />
                      <code>
                        docker pull {registry}/{selected}:
                        {tags.some((tag) => tag.name === "latest")
                          ? "latest"
                          : tags[0]?.name || "<tag>"}
                      </code>
                      <button
                        className="icon-button"
                        disabled={!tags.length}
                        onClick={() =>
                          void copy(
                            `docker pull ${registry}/${selected}:${tags.some((tag) => tag.name === "latest") ? "latest" : tags[0]?.name}`,
                          )
                        }
                        aria-label="Copy pull command"
                      >
                        <Copy size={15} />
                      </button>
                    </div>
                    <div className="tag-toolbar">
                      <div>
                        Tags<span>{tags.length}</span>
                      </div>
                      <label className="search tag-search">
                        <Search size={15} />
                        <input
                          aria-label="Filter tags"
                          placeholder="Filter tags…"
                          value={tagQuery}
                          onChange={(event) => setTagQuery(event.target.value)}
                        />
                      </label>
                    </div>
                    {tagError ? (
                      <div className="detail-message" role="alert">
                        <p>{tagError}</p>
                        <button
                          className="secondary"
                          onClick={() => setReload((value) => value + 1)}
                        >
                          Try again
                        </button>
                      </div>
                    ) : tagsLoading ? (
                      <div className="detail-message">
                        <LoaderCircle className="spin" size={23} />
                        <p>Loading image metadata…</p>
                      </div>
                    ) : (
                      <div className="table-scroll">
                        <table>
                          <thead>
                            <tr>
                              <th>Tag / digest</th>
                              <th>Platform</th>
                              <th>
                                Size{" "}
                                <span title="Compressed layer size; shared layers counted once per image">
                                  ⓘ
                                </span>
                              </th>
                              <th>Created</th>
                              <th>
                                <span className="sr-only">Actions</span>
                              </th>
                            </tr>
                          </thead>
                          <tbody>
                            {visibleTags.map((tag) => (
                              <tr key={tag.name}>
                                <td>
                                  <span className="tag-name">
                                    {tag.name}
                                    {tag.name === "latest" && (
                                      <span className="latest">latest</span>
                                    )}
                                  </span>
                                  <button
                                    className="digest"
                                    title={tag.digest}
                                    onClick={() =>
                                      tag.digest && void copy(tag.digest)
                                    }
                                    disabled={!tag.digest}
                                  >
                                    {tag.digest
                                      ? `${tag.digest.slice(0, 19)}…`
                                      : "No digest"}
                                    {tag.digest && <Copy size={11} />}
                                  </button>
                                  {tag.error && (
                                    <small
                                      className="metadata-error"
                                      title={tag.error}
                                    >
                                      {tag.error}
                                    </small>
                                  )}
                                </td>
                                <td>
                                  <span
                                    className="platform"
                                    title={tag.platforms.join(", ")}
                                  >
                                    {tag.platforms.length > 1
                                      ? `${tag.platforms.length} platforms`
                                      : tag.platforms[0] || "—"}
                                  </span>
                                </td>
                                <td className="size-cell">{bytes(tag.size)}</td>
                                <td className="date-cell" title={tag.created}>
                                  {date(tag.created)}
                                </td>
                                <td>
                                  <div className="row-actions">
                                    <button
                                      className="icon-button"
                                      aria-label={`Copy pull command for ${tag.name}`}
                                      onClick={() =>
                                        void copy(
                                          `docker pull ${registry}/${selected}:${tag.name}`,
                                        )
                                      }
                                    >
                                      <ArrowDownToLine size={15} />
                                    </button>
                                    <button
                                      className="icon-button danger"
                                      disabled={!tag.digest}
                                      aria-label={`Delete manifest for ${tag.name}`}
                                      onClick={() => {
                                        setDeleting(tag);
                                        setDeleteError("");
                                      }}
                                    >
                                      <Trash2 size={15} />
                                    </button>
                                  </div>
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                        {!visibleTags.length && (
                          <div className="detail-message">
                            <Package size={25} />
                            <p>
                              {tagQuery
                                ? "No matching tags."
                                : "No tags in this repository."}
                            </p>
                            {!tagQuery && (
                              <button
                                className="text-button"
                                onClick={() => {
                                  setImage(selected);
                                  setModal("push");
                                }}
                              >
                                Push your first image
                                <ArrowRight size={14} />
                              </button>
                            )}
                          </div>
                        )}
                      </div>
                    )}
                    <div className="detail-footer">
                      Sizes show compressed layers, not disk usage.
                      <span>Metadata fetched on refresh</span>
                    </div>
                  </>
                ) : (
                  <div className="empty-state">
                    <div className="empty-illustration">
                      <span />
                      <Box size={54} strokeWidth={1.2} />
                      <span />
                    </div>
                    <span className="eyebrow">READY WHEN YOU ARE</span>
                    <h2>A home for your images.</h2>
                    <p>
                      Push your first container image to start
                      <br />
                      building your private image library.
                    </p>
                    <button
                      className="primary"
                      onClick={() => setModal("push")}
                    >
                      <Plus size={16} />
                      Push your first image
                    </button>
                    <div className="empty-command">
                      <Terminal size={14} />
                      <code>docker push {registry}/my-app:latest</code>
                    </div>
                  </div>
                )}
              </div>
            </section>
            <footer className="page-footer">
              <span>
                <Box size={14} />
                Magistry · Your private image library
              </span>
              <button onClick={() => setModal("help")}>
                Need a hand?
                <ArrowRight size={14} />
              </button>
            </footer>
          </div>
        )}
      </main>

      {modal === "push" && (
        <Modal title="Push an image" onClose={() => setModal(null)}>
          <p className="dialog-intro">
            Tag a local image with your registry address, then push it with
            Docker.
          </p>
          <div className="image-inputs">
            <label>
              Local image / repository
              <input
                value={image}
                onChange={(event) => setImage(event.target.value)}
                placeholder="my-app"
              />
            </label>
            <label>
              Tag
              <input
                value={imageTag}
                onChange={(event) => setImageTag(event.target.value)}
                placeholder="latest"
              />
            </label>
          </div>
          {!validImage || !validTag ? (
            <p className="metadata-error">
              Enter a lowercase repository name and a valid Docker tag.
            </p>
          ) : (
            <div className="steps">
              {status?.authEnabled && (
                <>
                  <h3>Authenticate</h3>
                  {command(`docker login ${registry}`)}
                </>
              )}
              <h3>
                <span>1</span>Tag your local image
              </h3>
              {command(`docker tag ${imageRef} ${target}`)}
              <h3>
                <span>2</span>Push to your registry
              </h3>
              {command(`docker push ${target}`)}
            </div>
          )}
          <div className="dialog-note">
            <CircleHelp size={17} />
            <p>
              Docker push and pull use the same address as this app. After your
              push finishes, refresh the image library.
            </p>
          </div>
          <div className="dialog-actions">
            <button className="secondary" onClick={() => setModal(null)}>
              Close
            </button>
            <button
              className="primary"
              onClick={() => {
                setModal(null);
                void refresh();
              }}
            >
              <RefreshCw size={15} />
              Refresh library
            </button>
          </div>
        </Modal>
      )}
      {modal === "connection" && (
        <Modal title="Registry connection" onClose={() => setModal(null)}>
          <p className="dialog-intro">
            Use this address in Docker commands and your build pipelines.
          </p>
          <label className="field-label">REGISTRY ADDRESS</label>
          {command(registry)}
          <div className="connection-row">
            <span>Connection</span>
            <strong>
              <span className={`status-dot ${status ? "" : "offline"}`} />
              {status ? "Connected" : "Unavailable"}
            </strong>
          </div>
          <div className="connection-row">
            <span>Authentication</span>
            <strong>Browser session + Docker login</strong>
          </div>
          <div className="connection-row">
            <span>Image storage</span>
            <strong>Docker named volume</strong>
          </div>
          <div className="dialog-note">
            <Settings2 size={17} />
            <p>
              Signed in as {session.username}.{" "}
              {session.managed
                ? "Your administrator account is managed by the deployment environment."
                : "Your administrator account is stored on this server."}{" "}
              Use these credentials with Docker login.
            </p>
          </div>
          <div className="dialog-actions">
            <button className="primary" onClick={() => setModal(null)}>
              Done
              <Check size={15} />
            </button>
          </div>
        </Modal>
      )}
      {modal === "help" && (
        <Modal title="Quick reference" onClose={() => setModal(null)}>
          <p className="dialog-intro">
            A few commands to keep your images moving.
          </p>
          <div className="steps">
            <h3>Push an image</h3>
            {command(`docker tag my-app:latest ${registry}/my-app:latest`)}
            {command(`docker push ${registry}/my-app:latest`)}
            <h3>Pull an image</h3>
            {command(`docker pull ${registry}/my-app:latest`)}
            <h3>Log in from Docker</h3>
            {command(`docker login ${registry}`)}
          </div>
          <div className="dialog-note">
            <Trash2 size={17} />
            <p>
              Deleting a manifest removes all tags pointing to it. Storage is
              reclaimed separately with offline garbage collection; see the
              project README.
            </p>
          </div>
          <div className="dialog-actions">
            <button className="primary" onClick={() => setModal(null)}>
              Got it
            </button>
          </div>
        </Modal>
      )}
      {deleting && (
        <Modal
          title="Delete this manifest?"
          onClose={() => {
            if (!busy) setDeleting(null);
          }}
        >
          <p className="dialog-intro">
            This removes the manifest from <strong>{selected}</strong> and all
            tags pointing to it. This cannot be undone.
          </p>
          <div className="delete-tags">
            {tags
              .filter((tag) => tag.digest === deleting.digest)
              .map((tag) => (
                <span key={tag.name}>{tag.name}</span>
              ))}
          </div>
          <div className="delete-digest">
            <code>{deleting.digest}</code>
          </div>
          <div className="dialog-note">
            <CircleHelp size={17} />
            <p>
              Other tags may have been pushed since the last refresh. Every tag
              referencing this digest will be removed. Existing local images are
              unaffected.
            </p>
          </div>
          {deleteError && (
            <p role="alert" className="metadata-error">
              {deleteError}
            </p>
          )}
          <div className="dialog-actions">
            <button
              disabled={busy}
              className="secondary"
              onClick={() => setDeleting(null)}
            >
              Cancel
            </button>
            <button
              disabled={busy}
              className="destructive"
              onClick={() => void remove()}
            >
              {busy ? (
                <LoaderCircle size={15} className="spin" />
              ) : (
                <Trash2 size={15} />
              )}
              Delete manifest
            </button>
          </div>
        </Modal>
      )}
      {toast && (
        <div className="toast" role="status">
          <Check size={17} />
          {toast}
        </div>
      )}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Root
      render={(session, onLogout) => (
        <App session={session} onLogout={onLogout} />
      )}
    />
  </StrictMode>,
);

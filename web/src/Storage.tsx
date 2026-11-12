import { useCallback, useEffect, useState } from "react";
import {
  CircleHelp,
  Database,
  HardDrive,
  LoaderCircle,
  RefreshCw,
  Save,
  ScanSearch,
  Trash2,
} from "lucide-react";
import { api, json } from "./api";
import { Modal } from "./Modal";
type Usage = {
  usedBytes: number;
  blobBytes: number;
  uploadBytes: number;
  metadataBytes: number;
  blobs: number;
  filesystemBytes: number;
  availableBytes: number;
  limitBytes: number;
};
type Job = {
  state: string;
  dryRun: boolean;
  completed?: string;
  output?: string;
  error?: string;
  reclaimedBytes: number;
};
type Storage = { usage: Usage; job: Job };
const GiB = 1024 ** 3;
const format = (n: number) => {
  if (!n) return "0 B";
  const unit = Math.min(Math.floor(Math.log(n) / Math.log(1024)), 4);
  return `${(n / 1024 ** unit).toFixed(1)} ${["B", "KiB", "MiB", "GiB", "TiB"][unit]}`;
};
export function StoragePanel() {
  const [data, setData] = useState<Storage | null>(null);
  const [error, setError] = useState("");
  const [limit, setLimit] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const load = useCallback(async (initialize = false) => {
    try {
      const next = await api<Storage>("storage");
      setData(next);
      setError("");
      if (initialize)
        setLimit(
          next.usage.limitBytes ? String(next.usage.limitBytes / GiB) : "",
        );
    } catch (err) {
      setError((err as Error).message);
    }
  }, []);
  useEffect(() => {
    void load(true);
  }, [load]);
  useEffect(() => {
    if (data?.job.state !== "running") return;
    const timer = setInterval(() => void load(), 1500);
    return () => clearInterval(timer);
  }, [data?.job.state, load]);
  const running = data?.job.state === "running";
  async function saveLimit() {
    const value = limit.trim() ? Number(limit) * GiB : 0;
    if (
      !Number.isFinite(value) ||
      value < 0 ||
      !Number.isSafeInteger(Math.round(value))
    ) {
      setError(
        "Enter a valid limit in GiB, or leave blank for unlimited storage.",
      );
      return;
    }
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await api(
        "storage/limit",
        json("PUT", { limitBytes: Math.round(value) }),
      );
      await load();
      setSaved(true);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function cleanup(dryRun: boolean) {
    setBusy(true);
    setError("");
    try {
      const job = await api<Job>(
        "storage/cleanup",
        json("POST", { dryRun, confirm: !dryRun }),
      );
      setData((current) => (current ? { ...current, job } : current));
      setConfirm(false);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }
  const usage = data?.usage;
  return (
    <div className="content storage-content">
      <div className="page-heading">
        <div>
          <div className="eyebrow">REGISTRY STORAGE</div>
          <h1>
            Storage<span>.</span>
          </h1>
          <p>Monitor your image data and reclaim unused space.</p>
        </div>
        <button
          className="secondary"
          disabled={busy}
          onClick={() => void load()}
        >
          <RefreshCw size={15} />
          Refresh
        </button>
      </div>
      {error && (
        <div className="alert" role="alert">
          {error}
        </div>
      )}
      {!data && !error && (
        <div className="detail-message">
          <LoaderCircle size={24} className="spin" />
          <p>Reading registry storage…</p>
        </div>
      )}
      {usage && (
        <>
          <div className="storage-overview">
            <div>
              <span>
                <Database size={16} />
                Registry data
              </span>
              <strong>{format(usage.usedBytes)}</strong>
              <small>Blobs, uploads, and metadata</small>
            </div>
            <div>
              <span>
                <HardDrive size={16} />
                Stored blobs
              </span>
              <strong>{usage.blobs.toLocaleString()}</strong>
              <small>{format(usage.blobBytes)} of unique image data</small>
            </div>
            <div>
              <span>
                <HardDrive size={16} />
                Filesystem available
              </span>
              <strong>{format(usage.availableBytes)}</strong>
              <small>Shared Docker host filesystem</small>
            </div>
          </div>
          <section className="storage-section">
            <div className="storage-section-heading">
              <h2>Volume usage</h2>
              <span>
                {usage.limitBytes
                  ? `${format(usage.usedBytes)} of ${format(usage.limitBytes)} limit`
                  : "No storage limit"}
              </span>
            </div>
            <progress
              aria-label="Storage usage"
              value={usage.usedBytes}
              max={Math.max(
                usage.usedBytes,
                usage.limitBytes || usage.filesystemBytes,
                1,
              )}
            />
            <div className="usage-breakdown">
              <span>
                Image blobs<strong>{format(usage.blobBytes)}</strong>
              </span>
              <span>
                Pending uploads<strong>{format(usage.uploadBytes)}</strong>
              </span>
              <span>
                Registry metadata<strong>{format(usage.metadataBytes)}</strong>
              </span>
            </div>
            <p className="storage-caption">
              Shared layers are counted once. Filesystem space may also be used
              by other Docker volumes and images.
            </p>
          </section>
          <section className="storage-section">
            <div className="storage-section-heading">
              <div>
                <h2>Storage limit</h2>
                <p>
                  Control when the registry stops accepting new blob uploads.
                </p>
              </div>
            </div>
            <div className="limit-form">
              <label htmlFor="storage-limit">Limit in GiB</label>
              <div>
                <input
                  id="storage-limit"
                  type="number"
                  min="0"
                  step="any"
                  placeholder="Unlimited"
                  value={limit}
                  onChange={(event) => {
                    setLimit(event.target.value);
                    setSaved(false);
                  }}
                />
                <button
                  className="primary"
                  disabled={busy}
                  onClick={() => void saveLimit()}
                >
                  <Save size={15} />
                  Save limit
                </button>
                {saved && <span role="status">Limit saved</span>}
              </div>
              <small>Leave blank or enter 0 for unlimited storage.</small>
            </div>
            <div className="dialog-note">
              <CircleHelp size={17} />
              <p>
                This is a soft limit. Upload requests are checked against
                current disk usage; concurrent or unknown-size requests can
                exceed it. Existing images can still be pulled and deleted.
              </p>
            </div>
          </section>
          <section className="storage-section">
            <div className="storage-section-heading">
              <div>
                <h2>Reclaim unused data</h2>
                <p>Remove blobs no longer referenced by any stored manifest.</p>
              </div>
            </div>
            <div className="cleanup-actions">
              <button
                className="secondary"
                disabled={busy || running}
                onClick={() => void cleanup(true)}
              >
                {running ? (
                  <LoaderCircle className="spin" size={15} />
                ) : (
                  <ScanSearch size={15} />
                )}
                Analyze unused data
              </button>
              <button
                className="secondary cleanup-button"
                disabled={
                  busy ||
                  running ||
                  data.job.state !== "done" ||
                  !data.job.dryRun
                }
                onClick={() => setConfirm(true)}
              >
                <Trash2 size={15} />
                Reclaim unused blobs
              </button>
            </div>
            <p className="storage-caption">
              Registry traffic pauses during analysis and cleanup. In-progress
              pushes may need to be retried. Untagged manifests and
              multi-platform image references are preserved.
            </p>
            {data.job.state !== "idle" && (
              <div className="storage-job" role="status">
                <strong>
                  {running
                    ? data.job.dryRun
                      ? "Analyzing unused data…"
                      : "Reclaiming unused blobs…"
                    : data.job.state === "error"
                      ? "Storage job failed"
                      : data.job.dryRun
                        ? "Analysis complete"
                        : `Cleanup complete · ${format(data.job.reclaimedBytes)} reclaimed`}
                </strong>
                {data.job.error && (
                  <p className="form-error">{data.job.error}</p>
                )}
                {data.job.completed && (
                  <small>
                    Finished {new Date(data.job.completed).toLocaleString()}
                  </small>
                )}
                {data.job.output && (
                  <details>
                    <summary>View collector report</summary>
                    <pre>{data.job.output}</pre>
                  </details>
                )}
              </div>
            )}
          </section>
        </>
      )}
      {confirm && (
        <Modal
          title="Reclaim unused blobs?"
          onClose={() => {
            if (!busy) setConfirm(false);
          }}
        >
          <p className="dialog-intro">
            This permanently removes image data no longer referenced by any
            stored manifest. Tagged images, untagged manifests, and shared
            layers still in use are kept.
          </p>
          <div className="dialog-note">
            <CircleHelp size={17} />
            <p>
              Registry requests pause until cleanup finishes. Run this when no
              builds are pushing images.
            </p>
          </div>
          <div className="dialog-actions">
            <button
              className="secondary"
              disabled={busy}
              onClick={() => setConfirm(false)}
            >
              Cancel
            </button>
            <button
              className="destructive"
              disabled={busy}
              onClick={() => void cleanup(false)}
            >
              <Trash2 size={15} />
              Reclaim unused blobs
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

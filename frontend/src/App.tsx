import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Dispatch, FormEvent, RefObject, SetStateAction } from 'react';
import { ChooseFolder, CreateConfig, GetProjectDetails, RunProject, StopProject, TerminatePortOwnerAndRun } from '../wailsjs/go/main/ProjectManager';
import { EventsOn } from '../wailsjs/runtime/runtime';
import { Icon } from './icons';
import type { CreateProjectInput, ProcessIdentity, Project, ProjectDetails, ProjectLogEvent, ProjectStateEvent, WorkspaceState } from './types';
import './App.css';

type BusyAction = 'folder' | 'config' | 'run' | 'stop' | 'terminate' | null;
const projectLogEventName = 'project:log';
const projectStateEventName = 'project:state';

const emptyWorkspace: WorkspaceState = { folder: '', needsConfig: false, projects: [] };

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function folderName(path: string): string {
  const clean = path.replace(/[\\/]+$/, '');
  return clean.split(/[\\/]/).pop() || path;
}

function sameFolder(left: string, right: string): boolean {
  const normalize = (path: string) => path.replace(/[\\/]+$/, '').replace(/\\/g, '/');
  const normalizedLeft = normalize(left);
  const normalizedRight = normalize(right);
  if (normalizedLeft === normalizedRight) return true;
  return /^[a-z]:\//i.test(normalizedLeft) && /^[a-z]:\//i.test(normalizedRight)
    && normalizedLeft.toLowerCase() === normalizedRight.toLowerCase();
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function appendLogEvent(details: ProjectDetails, event: ProjectLogEvent): ProjectDetails {
  if (event.logSize <= details.logSize) return details;
  return { ...details, logLines: [...details.logLines, event.line].slice(-250), logSize: event.logSize };
}

function App() {
  const [workspace, setWorkspace] = useState<WorkspaceState>(emptyWorkspace);
  const [selectedProject, setSelectedProject] = useState('');
  const selectedProjectRef = useRef('');
  const [projectRunning, setProjectRunning] = useState<Record<string, boolean>>({});
  const projectStateVersions = useRef<Record<string, number>>({});
  const [details, setDetails] = useState<ProjectDetails | null>(null);
  const [busyAction, setBusyAction] = useState<BusyAction>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [portConflict, setPortConflict] = useState<ProcessIdentity | null>(null);
  const [portConflictProjectId, setPortConflictProjectId] = useState('');
  const [conflictError, setConflictError] = useState('');
  const [followLogs, setFollowLogs] = useState(true);
  const [form, setForm] = useState<CreateProjectInput>({ cwd: '.', run: 'go run .', port: '' });
  const logEndRef = useRef<HTMLDivElement>(null);

  const refreshDetails = useCallback(async (projectId: string, isCurrent: () => boolean = () => selectedProjectRef.current === projectId) => {
    const stateVersion = projectStateVersions.current[projectId] ?? 0;
    const next = await GetProjectDetails(projectId);
    if ((projectStateVersions.current[projectId] ?? 0) === stateVersion) {
      setProjectRunning((current) => ({ ...current, [projectId]: next.running }));
    }
    if (!isCurrent()) return next;
    setDetails((current) => {
      if (current?.id === projectId && current.logSize > next.logSize) {
        return { ...next, logLines: current.logLines, logSize: current.logSize };
      }
      return next;
    });
    return next;
  }, []);

  const selected = useMemo(
    () => workspace.projects.find((project) => project.id === selectedProject) ?? null,
    [workspace.projects, selectedProject],
  );

  const selectProjectId = useCallback((projectId: string) => {
    selectedProjectRef.current = projectId;
    setSelectedProject(projectId);
  }, []);

  const chooseFolder = useCallback(async () => {
    setBusyAction('folder');
    setError('');
    setNotice('');
    try {
      const result = await ChooseFolder();
      if (!result) return;
      setWorkspace(result);
      setPortConflict(null);
      setPortConflictProjectId('');
      setConflictError('');
      const previousProjectId = selectedProjectRef.current;
      const addedProject = result.projects.find((project) => sameFolder(project.folder, result.folder) || sameFolder(project.id, result.folder));
      const selectedStillExists = result.projects.some((project) => project.id === selectedProjectRef.current);
      let nextProjectId = selectedStillExists ? selectedProjectRef.current : result.projects[0]?.id ?? '';
      if (result.needsConfig) {
        nextProjectId = '';
        setForm({ cwd: '.', run: 'go run .', port: '' });
      } else if (addedProject) {
        nextProjectId = addedProject.id;
      }
      if (nextProjectId !== selectedProjectRef.current) setDetails(null);
      selectProjectId(nextProjectId);
      if (addedProject?.id === nextProjectId && addedProject.id === previousProjectId) {
        void refreshDetails(addedProject.id).catch((caught) => setError(errorMessage(caught)));
      }
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  }, [refreshDetails, selectProjectId]);

  useEffect(() => EventsOn(projectStateEventName, (event: ProjectStateEvent) => {
    projectStateVersions.current[event.projectId] = (projectStateVersions.current[event.projectId] ?? 0) + 1;
    setProjectRunning((current) => ({ ...current, [event.projectId]: event.running }));
  }), []);

  useEffect(() => {
    if (!selected) {
      setDetails(null);
      return;
    }
    const projectId = selected.id;
    let cancelled = false;
    let latestSnapshot: ProjectDetails | null = null;
    const pendingEvents: ProjectLogEvent[] = [];
    const unsubscribe = EventsOn(projectLogEventName, (event: ProjectLogEvent) => {
      if (cancelled || selectedProjectRef.current !== projectId || event.projectId !== projectId) return;
      if (!latestSnapshot) {
        pendingEvents.push(event);
        return;
      }
      latestSnapshot = appendLogEvent(latestSnapshot, event);
      setDetails((current) => {
        if (!current || current.id !== event.projectId) return current;
        return appendLogEvent(current, event);
      });
    });
    const unsubscribeState = EventsOn(projectStateEventName, (event: ProjectStateEvent) => {
      if (cancelled || selectedProjectRef.current !== projectId || event.projectId !== projectId) return;
      void refreshDetails(projectId, () => !cancelled && selectedProjectRef.current === projectId).then(() => {
        if (!cancelled) setError('');
      }).catch((caught) => {
        if (!cancelled) setError(errorMessage(caught));
      });
    });
    void refreshDetails(projectId, () => !cancelled && selectedProjectRef.current === projectId).then((initial) => {
      if (cancelled) return;
      latestSnapshot = pendingEvents
        .filter((event) => event.logSize > initial.logSize)
        .sort((left, right) => left.logSize - right.logSize)
        .reduce(appendLogEvent, initial);
      pendingEvents.length = 0;
      setDetails(latestSnapshot);
      setError('');
    }).catch((caught) => {
      if (!cancelled) setError(errorMessage(caught));
    });
    return () => {
      cancelled = true;
      unsubscribe();
      unsubscribeState();
    };
  }, [selected?.id, refreshDetails]);

  useEffect(() => {
    if (followLogs && details?.logLines.length) {
      logEndRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
    }
  }, [details?.logLines, followLogs]);

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(''), 3200);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const createConfig = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusyAction('config');
    setError('');
    try {
      const next = await CreateConfig(form);
      setWorkspace(next);
      const createdProject = next.projects.find((project) => sameFolder(project.folder, next.folder) || sameFolder(project.id, next.folder));
      const nextProjectId = createdProject?.id ?? next.projects[0]?.id ?? '';
      if (nextProjectId !== selectedProjectRef.current) setDetails(null);
      selectProjectId(nextProjectId);
      setNotice('nala.yml created');
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const runProject = async () => {
    if (!selected) return;
    const projectId = selected.id;
    const projectName = selected.name;
    setBusyAction('run');
    setError('');
    setNotice('');
    try {
      const result = await RunProject(projectId);
      if (result.portOwner) {
        if (selectedProjectRef.current === projectId) {
          setConflictError('');
          setPortConflictProjectId(projectId);
          setPortConflict(result.portOwner);
        }
      } else if (result.started) {
        if (selectedProjectRef.current === projectId) setNotice(`Started ${projectName}`);
        await refreshDetails(projectId);
      }
    } catch (caught) {
      const message = errorMessage(caught);
      if (message.toLowerCase().includes('already running')) {
        try {
          const current = await refreshDetails(projectId);
          if (current.running) {
            setError('');
            if (selectedProjectRef.current === projectId) setNotice(`${projectName} is already running`);
            return;
          }
        } catch (refreshError) {
          setError(errorMessage(refreshError));
          return;
        }
      }
      setError(message);
    } finally {
      setBusyAction(null);
    }
  };

  const terminateAndRun = async () => {
    if (!selected || !portConflict || selected.id !== portConflictProjectId) return;
    const projectId = selected.id;
    const projectName = selected.name;
    setBusyAction('terminate');
    setConflictError('');
    try {
      const result = await TerminatePortOwnerAndRun(projectId, portConflict.pid, portConflict.createTime);
      if (result.started) {
        setPortConflict(null);
        setPortConflictProjectId('');
        if (selectedProjectRef.current === projectId) setNotice(`Started ${projectName}`);
        await refreshDetails(projectId);
      } else if (result.portOwner) {
        setPortConflict(result.portOwner);
      }
    } catch (caught) {
      setConflictError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const stopProject = async () => {
    if (!selected) return;
    const projectId = selected.id;
    const projectName = selected.name;
    setBusyAction('stop');
    setError('');
    try {
      await StopProject(projectId);
      await refreshDetails(projectId);
      if (selectedProjectRef.current === projectId) setNotice(`Stopped ${projectName}`);
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusyAction(null);
    }
  };

  const selectProject = (project: Project) => {
    if (project.id === selectedProject) {
      setError('');
      setNotice('');
      void refreshDetails(project.id).catch((caught) => setError(errorMessage(caught)));
      return;
    }
    selectProjectId(project.id);
    setDetails(null);
    setError('');
    setNotice('');
    setPortConflict(null);
    setPortConflictProjectId('');
    setConflictError('');
  };

  const resumeSetup = () => {
    selectProjectId('');
    setDetails(null);
    setError('');
    setNotice('');
    setPortConflict(null);
    setPortConflictProjectId('');
    setConflictError('');
  };

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand-lockup" aria-label="Nala Ctrl">
          <div className="brand-mark"><span /></div>
          <div className="brand-type"><strong>nala<span>ctrl</span></strong><small>LOCAL PROCESS DESK</small></div>
        </div>

        <div className="workspace-label">LOCAL WORKSPACE</div>
        <button className="workspace-card" onClick={chooseFolder} disabled={busyAction === 'folder'} title="Add a project from another folder">
          <span className="workspace-card-icon"><Icon name="folder" /></span>
          <span className="workspace-card-copy">
            <strong>{selected ? folderName(selected.folder) : workspace.folder ? folderName(workspace.folder) : 'Add a project folder'}</strong>
            <small>{selected ? 'Add another project folder' : workspace.folder ? 'Folder selected' : 'Choose a folder with nala.yml'}</small>
          </span>
          <Icon className="workspace-chevron" name="folder-plus" />
        </button>
        {(selected?.folder || workspace.folder) && <div className="workspace-path" title={selected?.folder || workspace.folder}>{selected?.folder || workspace.folder}</div>}

        <div className="project-nav-head">
          <span>PROJECTS</span>
          <button className="add-project-button" onClick={chooseFolder} disabled={busyAction === 'folder'} title="Add project folder" aria-label="Add project folder"><Icon name="folder-plus" /></button>
        </div>
        <nav className="project-list" aria-label="Configured projects">
          {workspace.projects.map((project) => {
            const active = project.id === selectedProject;
            const live = projectRunning[project.id] ?? (active && details?.running);
            return (
              <button key={project.id} className={`project-item${active ? ' selected' : ''}`} onClick={() => selectProject(project)} aria-current={active ? 'page' : undefined} title={project.folder}>
                <span className={`project-dot${live ? ' live' : ''}`} />
                <span className="project-item-copy"><strong>{project.name}</strong><small>{project.port ? `PORT ${project.port}` : 'NO PORT CHECK'}</small><small className="project-location">{project.folder}</small></span>
                {active && <Icon className="project-chevron" name="chevron" />}
              </button>
            );
          })}
          {!workspace.projects.length && workspace.needsConfig && <div className="nav-empty">Finish setup to add this folder's project.</div>}
          {!workspace.projects.length && !workspace.folder && <div className="nav-empty">Add a project folder to get started.</div>}
        </nav>

        <div className="sidebar-bottom">
          <span className="local-indicator">LOCAL MACHINE</span>
          <span className="sidebar-footnote">Each added folder contributes one project. Switching projects leaves running processes alone.</span>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <div className="breadcrumb">
            <span>PROJECTS</span><Icon name="chevron" /><strong>{selected ? folderName(selected.folder) : workspace.folder ? folderName(workspace.folder) : 'No folder selected'}</strong>
            {selected && <><Icon name="chevron" /><span className="crumb-project">{selected.name}</span></>}
          </div>
          <div className="topbar-actions">
            {(workspace.folder || workspace.projects.length > 0) && <span className="local-pill">This machine</span>}
            <button className="icon-button" onClick={chooseFolder} title="Add project folder" aria-label="Add project folder" disabled={busyAction === 'folder'}><Icon name="folder-plus" /></button>
          </div>
        </header>

        <div className="content-scroll">
          {error && <div className="message-banner error-banner" role="alert"><Icon name="warning" /><span>{error}</span><button onClick={() => setError('')} aria-label="Dismiss error"><Icon name="close" /></button></div>}
          {notice && <div className="message-banner notice-banner" role="status"><Icon name="check" /><span>{notice}</span><button onClick={() => setNotice('')} aria-label="Dismiss notification"><Icon name="close" /></button></div>}

          {!workspace.folder && !workspace.projects.length && <WelcomeScreen onChoose={chooseFolder} busy={busyAction === 'folder'} />}
          {workspace.folder && workspace.needsConfig && !selected && (
            <SetupScreen folder={workspace.folder} form={form} setForm={setForm} onSubmit={createConfig} busy={busyAction === 'config'} />
          )}
          {workspace.folder && workspace.needsConfig && selected && (
            <div className="pending-setup-banner" role="status">
              <span><strong>{folderName(workspace.folder)} needs setup</strong><small>Existing projects keep running while you configure this folder.</small></span>
              <button className="secondary-button" onClick={resumeSetup}>Finish setup for {folderName(workspace.folder)}</button>
            </div>
          )}
          {workspace.folder && !workspace.needsConfig && workspace.projects.length === 0 && (
            <div className="empty-project-state">
              <div className="empty-glyph"><Icon name="server" /></div>
              <p className="eyebrow">NOTHING TO MANAGE</p>
              <h1>No project configured</h1>
              <p>Add a folder that contains <code>nala.yml</code> to monitor its configured project.</p>
              <button className="secondary-button" onClick={chooseFolder}><Icon name="folder-plus" /> Add project folder</button>
            </div>
          )}
          {selected && (
            <ProjectScreen
              project={selected}
              details={details}
              busyAction={busyAction}
              followLogs={followLogs}
              logEndRef={logEndRef}
              onRun={runProject}
              onStop={stopProject}
              onRefresh={() => void refreshDetails(selected.id).catch((caught) => setError(errorMessage(caught)))}
              onToggleFollow={() => setFollowLogs((value) => !value)}
            />
          )}
        </div>
      </main>

      {portConflict && selected && selected.id === portConflictProjectId && (
        <PortConflictDialog
          project={selected}
          owner={portConflict}
          error={conflictError}
          busy={busyAction === 'terminate'}
          onCancel={() => { setPortConflict(null); setPortConflictProjectId(''); setConflictError(''); }}
          onConfirm={terminateAndRun}
        />
      )}
    </div>
  );
}

function WelcomeScreen({ onChoose, busy }: { onChoose: () => void; busy: boolean }) {
  return (
    <section className="welcome-screen">
      <div className="welcome-content">
        <div className="welcome-emblem"><Icon name="terminal" /></div>
        <p className="eyebrow">A CLEAR VIEW OF WHAT'S RUNNING</p>
        <h1>Your project,<br /><em>under control.</em></h1>
        <p className="welcome-copy">Choose a project folder to see its status, follow its output, and start or stop it from one quiet place.</p>
        <button className="primary-button welcome-button" onClick={onChoose} disabled={busy}>
          <Icon name="folder-plus" /> {busy ? 'Opening folder…' : 'Choose project folder'}
        </button>
        <div className="welcome-note"><span className="note-line" /> Looks for <code>nala.yml</code> at the folder root</div>
      </div>
      <div className="welcome-footer"><span>LOCAL PROCESS MONITOR</span><span>ONE PROJECT PER CONFIG</span></div>
    </section>
  );
}

function SetupScreen({
  folder, form, setForm, onSubmit, busy,
}: {
  folder: string;
  form: CreateProjectInput;
  setForm: Dispatch<SetStateAction<CreateProjectInput>>;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  busy: boolean;
}) {
  const update = (key: keyof CreateProjectInput, value: string) => setForm((current) => ({ ...current, [key]: value }));
  return (
    <section className="setup-layout">
      <div className="setup-intro">
        <div className="setup-icon"><Icon name="folder-plus" /></div>
        <p className="eyebrow">FIRST RUN</p>
        <h1>Give this workspace<br />a starting point.</h1>
        <p>There’s no <code>nala.yml</code> here yet. Configure how to run this folder as a project named <strong>{folderName(folder)}</strong>.</p>
        <div className="setup-location"><Icon name="folder" /><span>{folder}</span></div>
      </div>

      <form className="setup-card" onSubmit={onSubmit}>
        <div className="setup-card-head"><div><span className="eyebrow">NEW CONFIGURATION</span><h2>Project setup</h2></div><span className="config-file-badge">nala.yml</span></div>
        <label className="field-label" htmlFor="project-cwd">Working directory</label>
        <input id="project-cwd" className="text-field mono-field" value={form.cwd} onChange={(event) => update('cwd', event.target.value)} placeholder="." required autoComplete="off" />
        <p className="field-help">Relative to the folder you chose, or an absolute path.</p>
        <label className="field-label" htmlFor="project-run">Run command</label>
        <input id="project-run" className="text-field mono-field" value={form.run} onChange={(event) => update('run', event.target.value)} placeholder="go run ." required autoComplete="off" />
        <label className="field-label" htmlFor="project-port">Port <span>OPTIONAL</span></label>
        <input id="project-port" className="text-field mono-field" value={form.port} onChange={(event) => update('port', event.target.value)} placeholder="Auto-detect from command" type="number" min="1" max="65535" />
        <p className="field-help">A clear port flag or common dev-server default is detected when this is blank.</p>
        <div className="config-preview"><span>CREATES</span><code>{folderName(folder)} · nala.yml · cwd · run · port</code></div>
        <button className="primary-button setup-submit" type="submit" disabled={busy}>{busy ? 'Creating config…' : 'Create nala.yml'}</button>
      </form>
    </section>
  );
}

function ProjectScreen({
  project, details, busyAction, followLogs, logEndRef, onRun, onStop, onRefresh, onToggleFollow,
}: {
  project: Project;
  details: ProjectDetails | null;
  busyAction: BusyAction;
  followLogs: boolean;
  logEndRef: RefObject<HTMLDivElement | null>;
  onRun: () => void;
  onStop: () => void;
  onRefresh: () => void;
  onToggleFollow: () => void;
}) {
  const running = details?.running ?? false;
  const owner = details?.portOwner ?? null;
  const lines = details?.logLines ?? [];
  const isBusy = busyAction === 'run' || busyAction === 'stop' || details === null;
  const processState = !details ? 'checking' : busyAction === 'run' ? 'starting' : busyAction === 'stop' ? 'stopping' : running ? 'running' : 'stopped';
  const processStateLabel = processState === 'checking' ? 'Checking process' : processState === 'starting' ? 'Starting process' : processState === 'stopping' ? 'Stopping process' : running ? 'Running' : 'Stopped';
  return (
    <section className="project-screen">
      <div className="project-heading">
        <div className="project-heading-copy">
          <div className="project-title-row">
            <span className={`large-status-dot${running ? ' live' : details ? ' stopped' : ' checking'}`} aria-hidden="true" />
            <div className="project-title-copy">
              <h1>{project.name}</h1>
              <div className={`process-status${running ? ' live' : ''}`} role="status" aria-live="polite">
                <strong>{processStateLabel}</strong>
                <span>{running ? `PID ${details?.pid}` : processState === 'checking' ? 'Reading process state' : owner ? `${owner.name} holds the port` : 'No managed process'}</span>
              </div>
            </div>
          </div>
          <p className="run-command"><Icon name="terminal" /><span>Run command</span><code title={project.run}>{project.run}</code></p>
        </div>
        <div className="project-actions">
          <button className="icon-button refresh-button" onClick={onRefresh} title="Refresh project" aria-label="Refresh project"><Icon name="refresh" /></button>
          {!details ? (
            <button className="secondary-button" disabled><Icon name="refresh" />Checking process…</button>
          ) : running ? (
            <button className="stop-button" onClick={onStop} disabled={isBusy}><Icon name="stop" />{busyAction === 'stop' ? 'Stopping…' : 'Stop project'}</button>
          ) : (
            <button className="run-button" onClick={onRun} disabled={isBusy}><Icon name="play" />{busyAction === 'run' ? 'Starting…' : 'Run project'}</button>
          )}
        </div>
      </div>

      <div className="project-meta" aria-label="Project details">
        <div className="project-meta-item">
          <span>PORT</span>
          <strong>{project.port ? `:${project.port}` : 'Not configured'}</strong>
          <small>{owner ? `${owner.name} · PID ${owner.pid}` : project.port ? 'Available' : 'Port check is not configured'}</small>
        </div>
        <div className="project-meta-item">
          <span>WORKING DIRECTORY</span>
          <code title={project.cwd}>{project.cwd}</code>
        </div>
      </div>

      <section className="log-panel" aria-label={`${project.name} log stream`}>
        <div className="log-panel-head">
          <div className="log-title"><Icon className="log-terminal-icon" name="terminal" /><div><h2>Output stream</h2><p>{details?.logPath ?? project.logPath}</p></div></div>
          <div className="log-controls">
            <span className="log-count">{details ? `${lines.length} lines · ${formatBytes(details.logSize)}` : 'Loading log…'}</span>
            <button className={`follow-button${followLogs ? ' following' : ''}`} onClick={onToggleFollow} aria-pressed={followLogs}><span className="follow-dot" />{followLogs ? 'Following' : 'Follow logs'}</button>
          </div>
        </div>
        <div className="log-screen" role="log" aria-live="off" aria-relevant="additions text">
          {lines.length ? lines.map((line, index) => (
            <div className="log-line" key={`${index}-${line}`}><span className="line-number">{String(index + 1).padStart(3, '0')}</span><span className="line-text">{line || ' '}</span></div>
          )) : (
            <div className="log-empty"><div className="log-empty-mark"><Icon name="terminal" /></div><strong>No output yet</strong><span>{running ? 'Waiting for the process to write its first line.' : 'Run this project to start capturing its output.'}</span></div>
          )}
          <div ref={logEndRef} />
        </div>
        <div className="log-panel-foot"><span><i className={running ? 'foot-live' : ''} />{running ? 'STREAM ACTIVE' : 'STREAM IDLE'}</span><span>Live output · latest 250 lines</span></div>
      </section>
    </section>
  );
}

function PortConflictDialog({
  project, owner, error, busy, onCancel, onConfirm,
}: {
  project: Project;
  owner: ProcessIdentity;
  error: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape' && !busy) onCancel(); };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [busy, onCancel]);

  return (
    <div className="dialog-scrim" onMouseDown={(event) => { if (event.target === event.currentTarget && !busy) onCancel(); }}>
      <section className="conflict-dialog" role="dialog" aria-modal="true" aria-labelledby="conflict-title">
        <div className="dialog-topline"><span className="warning-icon"><Icon name="warning" /></span><button className="dialog-close" onClick={onCancel} aria-label="Cancel" disabled={busy}><Icon name="close" /></button></div>
        <p className="eyebrow">PORT CONFLICT</p>
        <h2 id="conflict-title">Port {project.port} is already in use.</h2>
        <p className="dialog-copy">Nala Ctrl found a process listening on this port. Stop this process and run <strong>{project.name}</strong>?</p>
        <div className="process-identity">
          <div className="process-avatar"><Icon name="terminal" /></div>
          <div className="process-copy"><strong>{owner.name}</strong><span>{owner.managedProject ? `Managed project: ${owner.managedProject}` : 'Existing process on this machine'}</span></div>
          <div className="process-pid"><span>PID</span><strong>{owner.pid}</strong></div>
        </div>
        {!owner.canTerminate && <div className="cannot-terminate"><Icon name="warning" />{owner.terminationNote || 'This process cannot be terminated safely.'}</div>}
        {error && <div className="dialog-error" role="alert">{error}</div>}
        <div className="dialog-actions"><button className="secondary-button" onClick={onCancel} disabled={busy}>Cancel</button><button className="danger-button" onClick={onConfirm} disabled={busy || !owner.canTerminate}><Icon name="stop" />{busy ? 'Stopping process…' : 'Stop process & run'}</button></div>
        <p className="dialog-footnote">The process identity is checked again before it is stopped.</p>
      </section>
    </div>
  );
}

export default App;

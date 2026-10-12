export interface Project {
  name: string;
  cwd: string;
  run: string;
  port?: number | null;
  logPath: string;
}

export interface WorkspaceState {
  folder: string;
  needsConfig: boolean;
  projects: Project[];
}

export interface CreateProjectInput {
  cwd: string;
  run: string;
  port: string;
}

export interface ProcessIdentity {
  pid: number;
  name: string;
  createTime: number;
  canTerminate: boolean;
  managedProject: string;
  terminationNote: string;
}

export interface RunResult {
  started: boolean;
  pid: number;
  portOwner?: ProcessIdentity | null;
}

export interface ProjectDetails extends Project {
  running: boolean;
  pid: number;
  portOwner?: ProcessIdentity | null;
  logLines: string[];
  logSize: number;
}

export interface ProjectLogEvent {
  project: string;
  line: string;
  logSize: number;
}

export interface ProjectStateEvent {
  project: string;
  running: boolean;
  pid: number;
}

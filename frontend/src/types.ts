export interface Service {
  id: string;
  name: string;
  cwd: string;
  run: string;
  port?: number | null;
  logPath: string;
}

export interface Project {
  id: string;
  name: string;
  folder: string;
  services: Service[];
}

export interface WorkspaceState {
  folder: string;
  needsConfig: boolean;
  projects: Project[];
  warnings?: string[];
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
  managedService: string;
  terminationNote: string;
}

export interface RunResult {
  started: boolean;
  pid: number;
  portOwner?: ProcessIdentity | null;
}

export interface ProjectDetails {
  projectId: string;
  projectName: string;
  serviceId: string;
  serviceName: string;
  cwd: string;
  run: string;
  port?: number | null;
  logPath: string;
  running: boolean;
  pid: number;
  portOwner?: ProcessIdentity | null;
  logLines: string[];
  logSize: number;
}

export interface ProjectLogEvent {
  projectId: string;
  projectName: string;
  serviceId: string;
  serviceName: string;
  line: string;
  logSize: number;
}

export interface ProjectStateEvent {
  projectId: string;
  projectName: string;
  serviceId: string;
  serviceName: string;
  running: boolean;
  pid: number;
}

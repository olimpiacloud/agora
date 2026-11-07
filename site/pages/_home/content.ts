export type Locale = "en" | "es";

const commands = {
  install: "curl -fsSL https://agora.olimpia.dev/install.sh | sudo sh",
  claude: "claude mcp add agora -s user -- agora mcp",
  codex: "codex mcp add agora -- agora mcp",
  skill: "bunx skills add olimpiacloud/agora-skills",
};

const clients = (installLabel: string) => [
  { label: installLabel, icon: "terminal", command: commands.install },
  { label: "Claude Code", logo: "/agents/claude.svg", command: commands.claude },
  { label: "Codex", logo: "/agents/codex.svg", command: commands.codex },
  { label: "Skill", logo: "/agents/mcp.svg", invert: true, command: commands.skill },
];

export const home = {
  en: {
    title: "Agora by Olimpia: a real desktop for your agents",
    description:
      "Agora is an agent-native desktop runtime: one Linux desktop and one Chromium per workspace, shared by many AI agents with their own cursors, input seats and tabs. Open source, by Olimpia.",
    og: { image: "/og.jpg", alt: "Agora by Olimpia: a watercolor agora where agents share one desktop" },
    eyebrow: "Documentation · Open source",
    heading: "One desktop. Many agents.",
    lede: "Agora gives your AI agents a real Linux desktop and Chromium, shared by many cursors that each have their own input seat, tabs and token. These docs have two doors: one for your agent and one for you.",
    tablist: "Choose who the docs are for",
    agentsTab: "For agents",
    humansTab: "For humans",
    agents: {
      intro:
        "Agora ships an MCP server. Install the daemon, connect your agent once, and it browses, clicks, types and spawns subagents on its own desktop, handing control to you for CAPTCHAs and logins.",
      clients: clients("Install"),
      copy: "Copy the command for",
      machineIntro: "These docs also read without HTML: ",
      machine: [
        { href: "/llms.txt", label: "llms.txt" },
        { href: "/llms-full.txt", label: "llms-full.txt" },
        { href: "/skill.md", label: "skill.md" },
        { href: "/introduction.md", label: "Markdown for every page" },
      ],
      tasks: [
        { href: "/agents/connect", icon: "plug", title: "Connect", body: "The MCP server, the skill and tokens for Claude Code, Codex and any MCP client." },
        { href: "/agents/tools", icon: "wrench", title: "Tools", body: "Browser, desktop, cursor, workspace and board tools, and when to use each." },
        { href: "/agents/workflows", icon: "route", title: "Workflows", body: "End-to-end recipes: browse a site, split work across subagents, hand off to a human." },
      ],
      cta: { href: "/agents", label: "Guide for agents" },
    },
    humans: {
      guides: [
        { href: "/install", icon: "download", title: "Install", body: "One command on Ubuntu or Debian: the daemon, the browser and the systemd service." },
        { href: "/quickstart", icon: "rocket", title: "Quickstart", body: "Open the dashboard, create a workspace and give your agent a computer." },
        { href: "/concepts", icon: "layers", title: "Concepts", body: "Workspaces, cursors, input seats, human takeover and streaming." },
        { href: "/paseo", icon: "puzzle", title: "Paseo plugin", body: "Every Claude or Codex agent in Paseo gets its own cursor and a live desktop panel." },
        { href: "/security", icon: "shield-check", title: "Security", body: "Loopback only, scoped tokens, unprivileged apps and sandboxed workspaces." },
      ],
      cta: { href: "/introduction", label: "Read the introduction" },
    },
    art: {
      mobile: "/art/banner.webp",
      position: "object-center",
      agents: {
        src: "/art/banner.webp",
        alt: "Watercolor: a colonnaded agora with market stalls, browser windows and cursors",
        quote: "Many cursors, one desktop.",
        caption: "MCP server · 52 tools",
      },
      humans: {
        src: "/art/banner.webp",
        alt: "Watercolor: a colonnaded agora with market stalls, browser windows and cursors",
        quote: "What is built on stone, lasts.",
        caption: "Open source · by Olimpia",
      },
    },
    features: {
      eyebrow: "The runtime",
      heading: "What Agora gives your agents",
      more: { href: "/introduction", label: "See everything included" },
      items: [
        { icon: "mouse-pointer-2", title: "Cursors, not containers", body: "Many agents share one desktop and one browser, each with its own windows, pointer, keyboard and token.", href: "/concepts" },
        { icon: "globe", title: "Semantic browser control", body: "Snapshots with element refs, clicks and typing over CDP, and action tools that return what is visible.", href: "/agents/tools" },
        { icon: "monitor-play", title: "Live streaming", body: "Damage-driven H.264 over WebRTC or WebSocket, with a JPEG fallback, so you can watch every cursor.", href: "/concepts" },
        { icon: "users", title: "Subagents", body: "Spawn, pause, cancel and wait on child cursors, with permissions that only narrow and a shared board.", href: "/agents/workflows" },
        { icon: "hand", title: "Human takeover", body: "Agents ask for help with CAPTCHAs and logins and wait until you hand control back.", href: "/concepts" },
        { icon: "shield-check", title: "Secure by default", body: "Loopback only, scoped tokens, apps as an unprivileged user and optional sandboxed workspaces.", href: "/security" },
      ],
    },
  },
  es: {
    title: "Agora by Olimpia: un escritorio de verdad para tus agentes",
    description:
      "Agora es un runtime de escritorio hecho para agentes: un escritorio Linux y un Chromium por workspace, compartidos por muchos agentes de IA con su propio cursor, sus entradas y sus pestañas. Open source, de Olimpia.",
    og: { image: "/og.jpg", alt: "Agora by Olimpia: un ágora en acuarela donde los agentes comparten un escritorio" },
    eyebrow: "Documentación · Open source",
    heading: "Un escritorio. Muchos agentes.",
    lede: "Agora les da a tus agentes de IA un escritorio Linux de verdad y Chromium, compartidos por muchos cursores, cada uno con su teclado y mouse, sus pestañas y su token. Esta documentación tiene dos puertas: una para tu agente y otra para vos.",
    tablist: "Elegí para quién es la documentación",
    agentsTab: "Para agentes",
    humansTab: "Para humanos",
    agents: {
      intro:
        "Agora trae un servidor MCP. Instalá el daemon, conectá tu agente una vez y navega, hace clic, escribe y lanza subagentes en su propio escritorio, y te pasa el control para los CAPTCHAs y los logins.",
      clients: clients("Instalar"),
      copy: "Copiar el comando de",
      machineIntro: "Esta documentación también se lee sin HTML: ",
      machine: [
        { href: "/llms.txt", label: "llms.txt" },
        { href: "/llms-full.txt", label: "llms-full.txt" },
        { href: "/skill.md", label: "skill.md" },
        { href: "/es/introduction.md", label: "Markdown de cada página" },
      ],
      tasks: [
        { href: "/es/agents/connect", icon: "plug", title: "Conectar", body: "El servidor MCP, la skill y los tokens para Claude Code, Codex y cualquier cliente MCP." },
        { href: "/es/agents/tools", icon: "wrench", title: "Herramientas", body: "Herramientas de navegador, escritorio, cursores, workspaces y tablero, y cuándo usar cada una." },
        { href: "/es/agents/workflows", icon: "route", title: "Flujos", body: "Recetas de punta a punta: navegar un sitio, repartir trabajo entre subagentes, pasarle el control a una persona." },
      ],
      cta: { href: "/es/agents", label: "Guía para agentes" },
    },
    humans: {
      guides: [
        { href: "/es/install", icon: "download", title: "Instalar", body: "Un comando en Ubuntu o Debian: el daemon, el navegador y el servicio de systemd." },
        { href: "/es/quickstart", icon: "rocket", title: "Primeros pasos", body: "Abrí el dashboard, creá un workspace y dale una computadora a tu agente." },
        { href: "/es/concepts", icon: "layers", title: "Conceptos", body: "Workspaces, cursores, entradas, control humano y streaming." },
        { href: "/es/paseo", icon: "puzzle", title: "Plugin de Paseo", body: "Cada agente de Claude o Codex en Paseo tiene su propio cursor y un panel con el escritorio en vivo." },
        { href: "/es/security", icon: "shield-check", title: "Seguridad", body: "Solo loopback, tokens con alcance, apps sin privilegios y workspaces aislados." },
      ],
      cta: { href: "/es/introduction", label: "Leer la introducción" },
    },
    art: {
      mobile: "/art/banner.webp",
      position: "object-center",
      agents: {
        src: "/art/banner.webp",
        alt: "Acuarela: un ágora con columnas, puestos de mercado, ventanas de navegador y cursores",
        quote: "Muchos cursores, un escritorio.",
        caption: "Servidor MCP · 52 herramientas",
      },
      humans: {
        src: "/art/banner.webp",
        alt: "Acuarela: un ágora con columnas, puestos de mercado, ventanas de navegador y cursores",
        quote: "Lo que se construye sobre piedra, dura.",
        caption: "Open source · de Olimpia",
      },
    },
    features: {
      eyebrow: "El runtime",
      heading: "Lo que Agora les da a tus agentes",
      more: { href: "/es/introduction", label: "Ver todo lo que incluye" },
      items: [
        { icon: "mouse-pointer-2", title: "Cursores, no contenedores", body: "Muchos agentes comparten un escritorio y un navegador, cada uno con sus ventanas, su puntero, su teclado y su token.", href: "/es/concepts" },
        { icon: "globe", title: "Navegador semántico", body: "Snapshots con referencias a elementos, clics y escritura por CDP, y acciones que devuelven lo que se ve.", href: "/es/agents/tools" },
        { icon: "monitor-play", title: "Streaming en vivo", body: "H.264 por WebRTC o WebSocket guiado por los cambios en pantalla, con JPEG de respaldo, para ver cada cursor.", href: "/es/concepts" },
        { icon: "users", title: "Subagentes", body: "Lanzá, pausá, cancelá y esperá cursores hijos, con permisos que solo se achican y un tablero compartido.", href: "/es/agents/workflows" },
        { icon: "hand", title: "Control humano", body: "Los agentes piden ayuda con CAPTCHAs y logins y esperan hasta que les devolvés el control.", href: "/es/concepts" },
        { icon: "shield-check", title: "Seguro por defecto", body: "Solo loopback, tokens con alcance, apps como usuario sin privilegios y workspaces aislados opcionales.", href: "/es/security" },
      ],
    },
  },
};

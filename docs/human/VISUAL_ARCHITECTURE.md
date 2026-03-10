# Visual Architecture Guide

This document provides Mermaid diagrams to visualize the Golang home automation system architecture and logic flows.

## Table of Contents
- [System Architecture](#system-architecture)
- [Plugin System Architecture](#plugin-system-architecture)
- [State Synchronization Flow](#state-synchronization-flow)
- [Shadow State System](#shadow-state-system)
- [API Server Endpoints](#api-server-endpoints)
- [Reset Coordinator Flow](#reset-coordinator-flow)
- [Music Manager Logic Flow](#music-manager-logic-flow)
- [Lighting Control Logic Flow](#lighting-control-logic-flow)
- [Energy State Logic Flow](#energy-state-logic-flow)
- [State Variable Dependency Graph](#state-variable-dependency-graph)

---

## System Architecture

High-level view of the system components and their interactions.

```mermaid
graph TB
    subgraph "Home Automation Go Application"
        Main[cmd/main.go]

        subgraph "Core Layer"
            HAClient[HA WebSocket Client<br/>internal/ha/client.go]
            StateManager[State Manager<br/>internal/state/manager.go]
            Variables[State Variables<br/>internal/state/variables.go]
            Computed[Computed State<br/>internal/state/computed.go]
        end

        subgraph "Observability Layer"
            ShadowTracker[Shadow State Tracker<br/>internal/shadowstate/tracker.go]
            APIServer[HTTP API Server<br/>internal/api/server.go]
        end

        subgraph "Plugin Layer"
            StateTracking[State Tracking<br/>internal/plugins/statetracking/]
            DayPhase[Day Phase<br/>internal/plugins/dayphase/]
            Music[Music Manager<br/>internal/plugins/music/]
            Lighting[Lighting Manager<br/>internal/plugins/lighting/]
            Energy[Energy Manager<br/>internal/plugins/energy/]
            TV[TV Manager<br/>internal/plugins/tv/]
            Sleep[Sleep Hygiene<br/>internal/plugins/sleephygiene/]
            Security[Security Manager<br/>internal/plugins/security/]
            SexMode[Sex Mode<br/>internal/plugins/sexmode/]
            LoadShed[Load Shedding<br/>internal/plugins/loadshedding/]
            Christmas[Christmas<br/>internal/plugins/christmas/]
            Environmental[Environmental<br/>internal/plugins/environmental/]
            SensorHealth[Sensor Health<br/>internal/plugins/sensorhealth/]
            Infrastructure[Infrastructure<br/>internal/plugins/infrastructure/]
            WaterFlow[Water Flow<br/>internal/plugins/waterflow/]
            EVCharger[EV Charger Safety<br/>internal/plugins/evcharger/]
            ResetCoord[Reset Coordinator<br/>internal/plugins/reset/]
            SensorConfig[Sensor Config<br/>internal/plugins/sensorconfig/]
        end

        subgraph "Public Interfaces"
            PkgPlugin[pkg/plugin/interfaces.go]
            PkgHA[pkg/ha/interfaces.go]
            PkgState[pkg/state/interfaces.go]
        end

        subgraph "Configuration"
            ConfigLoader[Config Loader<br/>internal/config/loader.go]
            DayPhaseCalc[Day Phase Calculator<br/>internal/dayphase/calculator.go]
            Clock[Clock Interface<br/>internal/clock/clock.go]
        end
    end

    subgraph "External Systems"
        HA[Home Assistant<br/>WebSocket API]
        Sonos[Sonos Speakers]
        Hue[Phillips Hue]
        TV_Ext[Apple TV / LG TV]
        Ntfy[ntfy.sh<br/>Push Notifications]
        SoCoCLI[SoCo-CLI<br/>HTTP API]
    end

    Main --> HAClient
    Main --> StateManager
    Main --> ShadowTracker
    Main --> APIServer
    Main --> ConfigLoader

    HAClient <-->|WebSocket<br/>Auth, Commands,<br/>State Changes| HA

    StateManager -->|Read/Write<br/>State Variables| HAClient
    StateManager -.->|Subscribe to<br/>State Changes| Variables
    StateManager --> Computed

    APIServer -->|Query State| StateManager
    APIServer -->|Query Shadow| ShadowTracker

    %% Plugin connections
    StateTracking -->|Get/Set State| StateManager
    StateTracking -.->|Register Shadow| ShadowTracker

    DayPhase -->|Get/Set State| StateManager
    DayPhase -->|Use| DayPhaseCalc
    DayPhase -.->|Register Shadow| ShadowTracker

    Music -->|Get/Set State| StateManager
    Music -->|Call Services| HAClient
    Music -->|Speaker Commands<br/>& Tidal Playback| SoCoCLI
    Music -.->|Register Shadow| ShadowTracker

    Lighting -->|Get/Set State| StateManager
    Lighting -->|Call Services| HAClient
    Lighting -.->|Register Shadow| ShadowTracker

    Energy -->|Get/Set State| StateManager
    Energy -.->|Register Shadow| ShadowTracker

    TV -->|Get/Set State| StateManager
    TV -->|Call Services| HAClient
    TV -.->|Register Shadow| ShadowTracker

    Sleep -->|Get/Set State| StateManager
    Sleep -->|Call Services| HAClient
    Sleep -.->|Register Shadow| ShadowTracker

    Security -->|Get/Set State| StateManager
    Security -->|Call Services| HAClient
    Security -.->|Register Shadow| ShadowTracker

    SexMode -->|Get/Set State| StateManager
    SexMode -->|Call Services| HAClient
    SexMode -.->|Register Shadow| ShadowTracker

    LoadShed -->|Get/Set State| StateManager
    LoadShed -->|Call Services| HAClient
    LoadShed -.->|Register Shadow| ShadowTracker

    Christmas -->|Get/Set State| StateManager
    Christmas -->|Call Services| HAClient
    Christmas -.->|Register Shadow| ShadowTracker

    Environmental -->|Entity Subscriptions| HAClient
    Environmental -->|Notifications| Ntfy
    Environmental -.->|Register Shadow| ShadowTracker

    SensorHealth -->|Entity Subscriptions| HAClient
    SensorHealth -->|Notifications| Ntfy
    SensorHealth -.->|Register Shadow| ShadowTracker

    Infrastructure -->|Entity Subscriptions| HAClient
    Infrastructure -->|Notifications| Ntfy
    Infrastructure -->|TTS Announcements| HAClient
    Infrastructure -.->|Register Shadow| ShadowTracker

    WaterFlow -->|Entity Subscriptions| HAClient
    WaterFlow -->|Notifications| Ntfy
    WaterFlow -->|TTS Announcements| HAClient
    WaterFlow -.->|Register Shadow| ShadowTracker

    EVCharger -->|Entity Subscriptions| HAClient
    EVCharger -->|Call Services| HAClient
    EVCharger -->|Notifications| Ntfy
    EVCharger -->|TTS Announcements| HAClient
    EVCharger -.->|Register Shadow| ShadowTracker

    ResetCoord -->|Subscribe to reset| StateManager
    ResetCoord -.->|Reset All| StateTracking
    ResetCoord -.->|Reset All| Music
    ResetCoord -.->|Reset All| Lighting

    SensorConfig -->|Configure Thresholds| HAClient
    SensorConfig -.->|Register Shadow| ShadowTracker

    HA -->|Control| Sonos
    HA -->|Control| Hue
    HA -->|Monitor| TV_Ext

    style Main fill:#e1f5ff
    style HAClient fill:#fff3e0
    style StateManager fill:#fff3e0
    style ShadowTracker fill:#e8f5e9
    style APIServer fill:#e8f5e9
    style Music fill:#f3e5f5
    style Lighting fill:#f3e5f5
    style Energy fill:#f3e5f5
    style TV fill:#f3e5f5
    style Sleep fill:#f3e5f5
    style Security fill:#f3e5f5
    style SexMode fill:#f3e5f5
    style LoadShed fill:#f3e5f5
    style Christmas fill:#f3e5f5
    style Environmental fill:#f3e5f5
    style SensorHealth fill:#f3e5f5
    style StateTracking fill:#f3e5f5
    style DayPhase fill:#f3e5f5
    style WaterFlow fill:#f3e5f5
    style EVCharger fill:#f3e5f5
    style ResetCoord fill:#ffebee
    style SensorConfig fill:#f3e5f5
```

---

## Plugin System Architecture

The plugin system supports priority-based registration, allowing private implementations to override public plugins.

```mermaid
sequenceDiagram
    participant Main as cmd/main.go
    participant SM as State Manager
    participant HAC as HA Client
    participant ST as Shadow Tracker
    participant Plugin as Plugin Manager
    participant API as API Server
    participant HA as Home Assistant

    Main->>HAC: NewClient(url, token)
    Main->>SM: NewManager(haClient, logger, readOnly)
    Main->>SM: SyncFromHA()
    SM->>HAC: GetAllStates()
    HAC->>HA: Get All States (WS)
    HA-->>HAC: State Array
    HAC-->>SM: States
    SM->>SM: Parse & Cache All Variables
    SM->>SM: SetupComputedState()

    Main->>ST: NewTracker()
    Main->>API: NewServer(stateManager, shadowTracker, port)
    API->>API: Start HTTP Server

    Main->>Plugin: NewManager(haClient, stateManager, config)
    Main->>Plugin: Start()

    Plugin->>SM: Subscribe("dayPhase", handler)
    SM-->>Plugin: Subscription

    Plugin->>SM: Subscribe("isAnyoneHome", handler)
    SM-->>Plugin: Subscription

    Plugin->>ST: RegisterPluginProvider("music", getStateFunc)
    ST-->>Plugin: Registered

    Note over Plugin: Plugin is now monitoring state changes

    HA->>HAC: State Change Event (WS)
    HAC->>SM: Handler Callback
    SM->>SM: Update Cache
    SM->>Plugin: Notify Subscribed Handler
    Plugin->>Plugin: Business Logic
    Plugin->>Plugin: Update Shadow State
    Plugin->>SM: SetBool/SetString/SetNumber
    SM->>HAC: SetInputBoolean/Text/Number
    HAC->>HA: Call Service (WS)

    Note over API: HTTP Request arrives
    API->>SM: GetBool/GetString/GetNumber
    SM-->>API: State Values
    API->>ST: GetAllPluginStates()
    ST-->>API: Shadow States
    API-->>API: Return JSON Response
```

### Plugin Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Created: NewManager()
    Created --> Starting: Start()
    Starting --> Running: Subscriptions Active
    Running --> Resetting: Reset() called
    Resetting --> Running: Reset Complete
    Running --> Stopping: Stop()
    Stopping --> Stopped: Cleanup Complete
    Stopped --> [*]

    note right of Running
        Plugin monitors state changes
        and executes business logic
    end note

    note right of Resetting
        Plugin re-evaluates all
        conditions and recalculates state
    end note
```

### Plugin Interfaces

```mermaid
classDiagram
    class Plugin {
        <<interface>>
        +Name() string
        +Start() error
        +Stop()
    }

    class Resettable {
        <<interface>>
        +Reset() error
    }

    class ShadowStateProvider {
        <<interface>>
        +GetShadowState() PluginShadowState
    }

    class PluginInfo {
        +Name string
        +Description string
        +Priority int
        +Order int
        +Factory Factory
    }

    class Registry {
        -plugins map~string~PluginInfo
        +Register(info PluginInfo) error
        +Get(name string) *PluginInfo
        +List() []PluginInfo
        +CreateAll(ctx *Context) []Plugin
    }

    Plugin <|-- Resettable : optional
    Plugin <|-- ShadowStateProvider : optional
    Registry --> PluginInfo : manages
    PluginInfo --> Plugin : creates via Factory
```

---

## State Synchronization Flow

How state changes propagate through the system.

```mermaid
flowchart TD
    Start([State Change in HA]) --> WSEvent[WebSocket Event Received]
    WSEvent --> HAClient[HA Client Receives Event]
    HAClient --> ParseEvent{Parse Event Type}

    ParseEvent -->|state_changed| ExtractState[Extract Entity ID & New State]
    ParseEvent -->|Other| Ignore[Ignore Event]

    ExtractState --> FindSubs{Entity Has<br/>Subscriptions?}
    FindSubs -->|No| End1([End])
    FindSubs -->|Yes| CallHandlers[Call All Subscriber Handlers]

    CallHandlers --> SMHandler[State Manager Handler]
    SMHandler --> ParseValue[Parse State Value by Type]
    ParseValue --> TypeCheck{Variable Type?}

    TypeCheck -->|Boolean| ParseBool["Parse 'on'/'off' → true/false"]
    TypeCheck -->|Number| ParseNum[Parse String → Float64]
    TypeCheck -->|String| UseString[Use String Directly]
    TypeCheck -->|JSON| ParseJSON[Parse JSON String]

    ParseBool --> UpdateCache
    ParseNum --> UpdateCache
    UseString --> UpdateCache
    ParseJSON --> UpdateCache

    UpdateCache[Update State Manager Cache] --> RecomputeDerived{Triggers<br/>Computed State?}

    RecomputeDerived -->|Yes| Recompute[Recompute Derived Variables<br/>isAnyoneHomeAndAwake =<br/>isAnyOwnerHome AND !isAnyoneAsleep OR isToriHere OR wakeSequenceLatch]
    RecomputeDerived -->|No| NotifyPlugins

    Recompute --> SyncDerived[Sync Derived Value to HA]
    SyncDerived --> NotifyPlugins

    NotifyPlugins{Any Plugin<br/>Subscriptions?}

    NotifyPlugins -->|No| End2([End])
    NotifyPlugins -->|Yes| CallPluginHandlers[Call Plugin Handlers]

    CallPluginHandlers --> PluginLogic[Plugin Business Logic]
    PluginLogic --> UpdateShadow[Update Shadow State]
    UpdateShadow --> Decision{Plugin Needs<br/>to Update State?}

    Decision -->|No| End3([End])
    Decision -->|Yes| SetState[Plugin Calls SetBool/SetString/SetNumber]

    SetState --> CheckReadOnly{Read-Only<br/>Mode?}
    CheckReadOnly -->|Yes| LogOnly[Log Would-Be Change]
    CheckReadOnly -->|No| SyncToHA[Sync to Home Assistant]

    LogOnly --> End4([End])
    SyncToHA --> CallService[Call HA Service via WebSocket]
    CallService --> End5([End])

    style Start fill:#e1f5ff
    style UpdateCache fill:#fff3e0
    style RecomputeDerived fill:#fff3e0
    style Recompute fill:#e8f5e9
    style PluginLogic fill:#e8f5e9
    style UpdateShadow fill:#e8f5e9
    style CallService fill:#ffebee
```

---

## Shadow State System

Shadow state captures the decision-making context for each plugin, enabling debugging and observability.

```mermaid
graph TB
    subgraph "Shadow State Tracker"
        Tracker[Tracker<br/>shadowstate/tracker.go]
        PluginStates[pluginStates<br/>map~string~PluginShadowState]
        Providers[stateProviders<br/>map~string~func]
    end

    subgraph "Plugin Shadow States"
        LightingShadow[LightingShadowState<br/>- Inputs: current, atLastAction<br/>- Outputs: rooms, scenes<br/>- Metadata]

        MusicShadow[MusicShadowState<br/>- Inputs: current, atLastAction<br/>- Outputs: mode, playlist, speakers<br/>- Metadata]

        SecurityShadow[SecurityShadowState<br/>- Inputs: current, atLastAction<br/>- Outputs: lockdown, doorbell, garage<br/>- Metadata]

        SexModeShadow[SexModeShadowState<br/>- Inputs: current, atLastAction<br/>- Outputs: isActive, preSexMusicType<br/>- Metadata]

        EnergyShadow[EnergyShadowState<br/>- Inputs: current<br/>- Outputs: levels, sensor readings<br/>- Metadata]

        StateTrackingShadow[StateTrackingShadowState<br/>- Inputs: current<br/>- Outputs: derived states, timers<br/>- Metadata]

        DayPhaseShadow[DayPhaseShadowState<br/>- Inputs: current<br/>- Outputs: sunEvent, dayPhase<br/>- Metadata]
    end

    subgraph "API Server"
        APIEndpoint["API: /api/shadow/*"]
    end

    Tracker --> PluginStates
    Tracker --> Providers

    Providers --> LightingShadow
    Providers --> MusicShadow
    Providers --> SecurityShadow
    Providers --> SexModeShadow
    Providers --> EnergyShadow
    Providers --> StateTrackingShadow
    Providers --> DayPhaseShadow

    APIEndpoint -->|GetAllPluginStates| Tracker
    APIEndpoint -->|GetPluginState| Tracker

    style Tracker fill:#e1f5ff
    style APIEndpoint fill:#e8f5e9
```

### Shadow State Interface

```mermaid
classDiagram
    class PluginShadowState {
        <<interface>>
        +GetCurrentInputs() map~string~interface
        +GetLastActionInputs() map~string~interface
        +GetOutputs() interface
        +GetMetadata() StateMetadata
    }

    class StateMetadata {
        +LastUpdated time.Time
        +PluginName string
    }

    class ShadowState~I,O~ {
        +Plugin string
        +Inputs I
        +Outputs O
        +Metadata StateMetadata
    }

    class ActionInputs {
        +Current map~string~interface
        +AtLastAction map~string~interface
    }

    class ReadOnlyInputs {
        +Current map~string~interface
    }

    class ActionTracker~O~ {
        +UpdateCurrentInputs()
        +SnapshotInputsForAction()
        +GetState()
    }

    class ReadOnlyTracker~O~ {
        +UpdateCurrentInputs()
        +GetState()
    }

    PluginShadowState <|.. ShadowState~I,O~
    ShadowState~I,O~ --> StateMetadata
    ShadowState~I,O~ --> ActionInputs : action-heavy plugins
    ShadowState~I,O~ --> ReadOnlyInputs : read-heavy plugins
    ActionTracker~O~ --> ShadowState~I,O~
    ReadOnlyTracker~O~ --> ShadowState~I,O~
```

---

## API Server Endpoints

The HTTP API server provides observability into the system state.

```mermaid
graph LR
    subgraph "HTTP API Server :8080"
        Root["GET /"]
        Health["GET /health"]
        State["GET /api/state"]
        States["GET /api/states"]
        Shadow["GET /api/shadow"]
        ShadowLighting["GET /api/shadow/lighting"]
        ShadowMusic["GET /api/shadow/music"]
        ShadowSecurity["GET /api/shadow/security"]
        ShadowEnergy["GET /api/shadow/energy"]
        ShadowLoadShed["GET /api/shadow/loadshedding"]
        ShadowSleep["GET /api/shadow/sleephygiene"]
        ShadowState["GET /api/shadow/statetracking"]
        ShadowDayPhase["GET /api/shadow/dayphase"]
        ShadowTV["GET /api/shadow/tv"]
        ShadowSexMode["GET /api/shadow/sexmode"]
    end

    subgraph "Response Types"
        Sitemap[Sitemap<br/>HTML/Text]
        HealthCheck["Health Check<br/>status: ok"]
        AllState[All Variables<br/>by Type]
        ByPlugin[Variables<br/>by Plugin]
        AllShadow[All Plugin<br/>Shadow States]
        PluginShadow[Single Plugin<br/>Shadow State]
    end

    Root --> Sitemap
    Health --> HealthCheck
    State --> AllState
    States --> ByPlugin
    Shadow --> AllShadow
    ShadowLighting --> PluginShadow
    ShadowMusic --> PluginShadow
    ShadowSecurity --> PluginShadow
    ShadowEnergy --> PluginShadow
    ShadowLoadShed --> PluginShadow
    ShadowSleep --> PluginShadow
    ShadowState --> PluginShadow
    ShadowDayPhase --> PluginShadow
    ShadowTV --> PluginShadow
    ShadowSexMode --> PluginShadow

    style Root fill:#e1f5ff
    style Health fill:#e8f5e9
    style State fill:#fff3e0
    style States fill:#fff3e0
    style Shadow fill:#f3e5f5
```

### API Response Structure

```mermaid
classDiagram
    class StateResponse {
        +Booleans map~string~bool
        +Numbers map~string~float64
        +Strings map~string~string
        +JSONs map~string~any
    }

    class PluginStatesResponse {
        +Plugins map~string~map~string~PluginStateValue
    }

    class PluginStateValue {
        +Value interface
        +Type string
    }

    class AllShadowStatesResponse {
        +Plugins map~string~interface
        +Metadata ShadowMetadata
    }

    class ShadowMetadata {
        +Timestamp time.Time
        +Version string
    }

    PluginStatesResponse --> PluginStateValue
    AllShadowStatesResponse --> ShadowMetadata
```

---

## Reset Coordinator Flow

The Reset Coordinator watches for the `reset` boolean and orchestrates system-wide resets.

```mermaid
flowchart TD
    Start([Reset Boolean = true<br/>in Home Assistant]) --> Subscribe[Reset Coordinator<br/>Subscribed to 'reset']

    Subscribe --> HandleChange["handleResetChange()"]
    HandleChange --> CheckValue{newValue == true?}

    CheckValue -->|No| End1([End - No Action])
    CheckValue -->|Yes| LogStart["Log: Reset triggered"]

    LogStart --> TurnOff{Read-Only Mode?}
    TurnOff -->|Yes| LogOnly1["Log: Would turn reset off"]
    TurnOff -->|No| SetFalse["Set reset = false"]

    LogOnly1 --> Execute
    SetFalse --> Execute

    Execute["executeReset()"] --> ForEach[For Each Plugin]

    ForEach --> Plugin1[Reset State Tracking]
    Plugin1 --> Plugin2[Reset Day Phase]
    Plugin2 --> Plugin3[Reset Energy]
    Plugin3 --> Plugin4[Reset Load Shedding]
    Plugin4 --> Plugin5[Reset Lighting]
    Plugin5 --> Plugin6[Reset Music]
    Plugin6 --> Plugin7[Reset Security]
    Plugin7 --> Plugin8[Reset Sleep Hygiene]

    Plugin8 --> Summary[Log Summary:<br/>success/error counts]
    Summary --> End2([Reset Complete])

    subgraph "Plugin Reset Actions"
        ResetAction[Each Plugin Reset:<br/>1. Clear rate limiters<br/>2. Re-evaluate conditions<br/>3. Recalculate state<br/>4. Update shadow state]
    end

    Plugin1 -.-> ResetAction
    Plugin5 -.-> ResetAction
    Plugin6 -.-> ResetAction

    style Start fill:#e1f5ff
    style Execute fill:#fff3e0
    style Plugin1 fill:#e8f5e9
    style Plugin2 fill:#e8f5e9
    style Plugin3 fill:#e8f5e9
    style Plugin4 fill:#e8f5e9
    style Plugin5 fill:#e8f5e9
    style Plugin6 fill:#e8f5e9
    style Plugin7 fill:#e8f5e9
    style Plugin8 fill:#e8f5e9
    style End2 fill:#c8e6c9
```

---

## Music Manager Logic Flow

Zone-based music orchestration. All music selection is driven by zone trigger evaluation — there is no separate decision tree. Configs without explicit zones auto-generate them at load time via `ensureZones()` (PR #639).

```mermaid
flowchart TD
    Start([State Change Detected:<br/>dayPhase, isAnyoneHome,<br/>isAnyoneAsleep, etc.]) --> ZoneResolve[Zone Manager:<br/>ResolveZones]

    ZoneResolve --> Snapshot[Capture State Snapshot]
    Snapshot --> EvalZones[Evaluate All Zone Triggers]

    EvalZones --> SleepZone{sleep zone:<br/>isAnyoneAsleep=true<br/>isAnyoneHome=true}
    EvalZones --> MorningZone{morning zone:<br/>dayPhase=morning<br/>isAnyoneHome=true<br/>isAnyoneAsleep=false}
    EvalZones --> DayZone{day zone:<br/>dayPhase=day<br/>+ home & awake}
    EvalZones --> EveningZone{evening zone:<br/>dayPhase=sunset/dusk/evening<br/>+ home & awake}
    EvalZones --> WinddownZone{winddown zone:<br/>dayPhase=winddown/night<br/>+ home & awake}

    SleepZone -->|Match| Priority[Sort by Priority]
    MorningZone -->|Match| Priority
    DayZone -->|Match| Priority
    EveningZone -->|Match| Priority
    WinddownZone -->|Match| Priority
    SleepZone -->|No Match| Skip1([Skip])
    MorningZone -->|No Match| Skip2([Skip])
    DayZone -->|No Match| Skip3([Skip])
    EveningZone -->|No Match| Skip4([Skip])
    WinddownZone -->|No Match| Skip5([Skip])

    Priority --> AssignSpeakers[Assign Speakers to Zones<br/>Higher priority wins conflicts]
    AssignSpeakers --> Changes{Zone Changes?}

    Changes -->|Zones to Stop| StopZone[Stop Zone:<br/>Fade out speakers]
    Changes -->|Zones to Start| StartZone[Start Zone:<br/>Orchestrate playback]
    Changes -->|No Change| Done([Done])

    StartZone --> SelectPlaylist[Select Playlist with Rotation<br/>from music_config.yaml]
    SelectPlaylist --> BreakGroups[Break Existing Speaker Groups<br/>via SoCo-CLI ungroup]
    BreakGroups --> BuildGroup[Build Sonos Speaker Group<br/>via SoCo-CLI group]
    BuildGroup --> MuteAll[Mute All Speakers to 0<br/>via SoCo-CLI volume]
    MuteAll --> StartPlayback{Media Type?}
    StartPlayback -->|Spotify| SoCoPlayURI[Start via SoCo-CLI play_uri]
    StartPlayback -->|Tidal| SoCoPlay[Start via SoCo-CLI<br/>sharelink + play_from_queue]
    SoCoPlayURI --> VerifyPlay[Verify Playback Started]
    SoCoPlay --> VerifyPlay
    VerifyPlay --> EnableShuffle[Enable Shuffle for Playlists]
    EnableShuffle --> EvalConditions[Evaluate Mute Conditions<br/>for Each Speaker]
    EvalConditions --> FadeIn[Fade In Eligible Speakers<br/>Gradually 0→targetVolume]
    FadeIn --> UpdateShadow[Update Shadow State:<br/>mode, playlist, speakers]
    UpdateShadow --> Complete([Playback Complete])

    StopZone --> Done

    style Start fill:#e1f5ff
    style ZoneResolve fill:#e1f5ff
    style SleepZone fill:#fff3e0
    style MorningZone fill:#fff3e0
    style DayZone fill:#fff3e0
    style EveningZone fill:#fff3e0
    style WinddownZone fill:#fff3e0
    style SelectPlaylist fill:#e8f5e9
    style BreakGroups fill:#e8f5e9
    style StartPlayback fill:#e8f5e9
    style UpdateShadow fill:#f3e5f5
```

**Reference:** See `homeautomation-go/internal/plugins/music/zone_manager.go` and `config.go` for implementation details.

**Zone Resolution:**
1. State change triggers `ResolveZones` in the zone manager
2. All zone trigger conditions are evaluated against current state
3. Matching zones are sorted by priority (sleep=100 > morning=50 > day/evening=40)
4. Speakers are assigned to highest-priority zone (no speaker shared between zones)
5. Zones that lost all speakers are stopped; new zones are started

**Playback Sequence (per zone):**
1. Select playlist with rotation
2. Break existing speaker groups (SoCo-CLI ungroup)
3. Build new speaker group (SoCo-CLI group)
4. Mute all speakers to 0 (SoCo-CLI volume)
5. Start playback on lead player (Spotify via SoCo-CLI play_uri; Tidal via SoCo-CLI sharelink)
6. Enable shuffle for playlists (SoCo-CLI shuffle)
7. Fade in eligible speakers (SoCo-CLI volume)

> **Note:** All speaker commands route through SoCo-CLI (direct UPnP) when configured. State reads (current volume, playback status) still go through Home Assistant.

---

## Lighting Control Logic Flow

Scene activation based on day phase and conditional logic (matches Node-RED Lighting Control flow).

```mermaid
flowchart TD
    Start([State Change:<br/>dayPhase or<br/>isAnyoneHome or<br/>isAnyoneAsleep]) --> GetState[Get Current State:<br/>dayPhase<br/>isAnyoneHome<br/>isAnyoneAsleep<br/>isTVPlaying]

    GetState --> LoadConfig[Load hue_config.yaml<br/>Scene Configurations]

    LoadConfig --> UpdateShadow1[Update Shadow State:<br/>Current Inputs]

    UpdateShadow1 --> IterateRooms[For Each Room in Config]

    IterateRooms --> CheckConditions{Evaluate Room<br/>Conditions}

    CheckConditions -->|on_if_true matched| GetSceneOn1[Get Scene Name<br/>from Config]
    CheckConditions -->|on_if_false matched| GetSceneOn2[Get Scene Name<br/>from Config]
    CheckConditions -->|default| GetSceneDefault[Get Default Scene<br/>for dayPhase]
    CheckConditions -->|off_if_true matched| TurnOff1[Turn Room Off]
    CheckConditions -->|off_if_false matched| TurnOff2[Turn Room Off]

    GetSceneOn1 --> FormatScene1[Format Scene Name:<br/>room/dayPhase]
    GetSceneOn2 --> FormatScene2[Format Scene Name:<br/>room/dayPhase]
    GetSceneDefault --> FormatScene3[Format Scene Name:<br/>room/dayPhase]

    FormatScene1 --> ActivateScene1[Call scene.turn_on<br/>entity_id: scene.ROOM_SCENE]
    FormatScene2 --> ActivateScene2[Call scene.turn_on<br/>entity_id: scene.ROOM_SCENE]
    FormatScene3 --> ActivateScene3[Call scene.turn_on<br/>entity_id: scene.ROOM_SCENE]

    TurnOff1 --> CallLightOff1[Call light.turn_off<br/>for room entities]
    TurnOff2 --> CallLightOff2[Call light.turn_off<br/>for room entities]

    ActivateScene1 --> RecordAction1[Record Room Action<br/>in Shadow State]
    ActivateScene2 --> RecordAction2[Record Room Action<br/>in Shadow State]
    ActivateScene3 --> RecordAction3[Record Room Action<br/>in Shadow State]
    CallLightOff1 --> RecordAction4[Record Room Action<br/>in Shadow State]
    CallLightOff2 --> RecordAction5[Record Room Action<br/>in Shadow State]

    RecordAction1 --> NextRoom1{More Rooms?}
    RecordAction2 --> NextRoom2{More Rooms?}
    RecordAction3 --> NextRoom3{More Rooms?}
    RecordAction4 --> NextRoom4{More Rooms?}
    RecordAction5 --> NextRoom5{More Rooms?}

    NextRoom1 -->|Yes| IterateRooms
    NextRoom2 -->|Yes| IterateRooms
    NextRoom3 -->|Yes| IterateRooms
    NextRoom4 -->|Yes| IterateRooms
    NextRoom5 -->|Yes| IterateRooms

    NextRoom1 -->|No| Complete([All Scenes Updated])
    NextRoom2 -->|No| Complete
    NextRoom3 -->|No| Complete
    NextRoom4 -->|No| Complete
    NextRoom5 -->|No| Complete

    style Start fill:#e1f5ff
    style CheckConditions fill:#fff3e0
    style ActivateScene1 fill:#e8f5e9
    style ActivateScene2 fill:#e8f5e9
    style ActivateScene3 fill:#e8f5e9
    style RecordAction1 fill:#f3e5f5
    style RecordAction2 fill:#f3e5f5
    style RecordAction3 fill:#f3e5f5
```

**Reference:** See `homeautomation-go/internal/plugins/lighting/manager.go` for implementation details.

**Condition Evaluation Logic:**
- `on_if_true`: Activate scene if ALL specified state variables are true
- `on_if_false`: Activate scene if ALL specified state variables are false
- `off_if_true`: Turn off room if ALL specified state variables are true
- `off_if_false`: Turn off room if ALL specified state variables are false
- Conditions are evaluated in order of precedence: off conditions → on conditions → default

---

## Energy State Logic Flow

Battery level calculation and energy state management (matches Node-RED Energy State flow).

```mermaid
flowchart TD
    Start([HA Sensor Update:<br/>sensor.span_battery_charge_percent]) --> GetBatteryPercent[Get Battery Charge %]

    GetBatteryPercent --> LoadConfig[Load energy_config.yaml<br/>Battery Level Thresholds]

    LoadConfig --> UpdateShadow1[Update Shadow State:<br/>Sensor Readings]

    UpdateShadow1 --> CheckLevels{Compare Battery %<br/>to Thresholds}

    CheckLevels -->|< critical_threshold| SetCritical[batteryEnergyLevel = 'critical']
    CheckLevels -->|< low_threshold| SetLow[batteryEnergyLevel = 'low']
    CheckLevels -->|< medium_threshold| SetMedium[batteryEnergyLevel = 'medium']
    CheckLevels -->|< high_threshold| SetHigh[batteryEnergyLevel = 'high']
    CheckLevels -->|>= high_threshold| SetFull[batteryEnergyLevel = 'full']

    SetCritical --> UpdateBattery1[Update Shadow State:<br/>Battery Level]
    SetLow --> UpdateBattery2[Update Shadow State:<br/>Battery Level]
    SetMedium --> UpdateBattery3[Update Shadow State:<br/>Battery Level]
    SetHigh --> UpdateBattery4[Update Shadow State:<br/>Battery Level]
    SetFull --> UpdateBattery5[Update Shadow State:<br/>Battery Level]

    UpdateBattery1 --> SyncToHA1[Sync to HA:<br/>input_text.battery_energy_level]
    UpdateBattery2 --> SyncToHA2[Sync to HA:<br/>input_text.battery_energy_level]
    UpdateBattery3 --> SyncToHA3[Sync to HA:<br/>input_text.battery_energy_level]
    UpdateBattery4 --> SyncToHA4[Sync to HA:<br/>input_text.battery_energy_level]
    UpdateBattery5 --> SyncToHA5[Sync to HA:<br/>input_text.battery_energy_level]

    SyncToHA1 --> CalculateCurrent
    SyncToHA2 --> CalculateCurrent
    SyncToHA3 --> CalculateCurrent
    SyncToHA4 --> CalculateCurrent
    SyncToHA5 --> CalculateCurrent

    CalculateCurrent[Calculate currentEnergyLevel] --> CheckFreeEnergy{isFreeEnergyAvailable?}

    CheckFreeEnergy -->|Yes| SetInfinite[currentEnergyLevel = 'infinite']
    CheckFreeEnergy -->|No| CheckGrid{isGridAvailable?}

    CheckGrid -->|Yes & battery high/full| SetAbundant[currentEnergyLevel = 'abundant']
    CheckGrid -->|Yes & battery medium| SetPlenty[currentEnergyLevel = 'plenty']
    CheckGrid -->|No or battery low/critical| UseBattery[currentEnergyLevel = batteryEnergyLevel]

    SetInfinite --> UpdateOverall[Update Shadow State:<br/>Overall Level]
    SetAbundant --> UpdateOverall
    SetPlenty --> UpdateOverall
    UseBattery --> UpdateOverall

    UpdateOverall --> End([End])

    style Start fill:#e1f5ff
    style CheckLevels fill:#fff3e0
    style CheckFreeEnergy fill:#fff3e0
    style CheckGrid fill:#fff3e0
    style SetInfinite fill:#c8e6c9
    style SetAbundant fill:#e8f5e9
    style UseBattery fill:#ffebee
    style UpdateShadow1 fill:#f3e5f5
    style UpdateBattery1 fill:#f3e5f5
    style UpdateOverall fill:#f3e5f5
```

**Reference:** See `homeautomation-go/internal/plugins/energy/manager.go` for implementation details.

---

## State Variable Dependency Graph

Shows which plugins read/write which state variables (41 total: 28 booleans, 3 numbers, 8 strings, 2 local-only).

```mermaid
graph LR
    subgraph "Input State Variables"
        NickHome[isNickHome]
        CarolineHome[isCarolineHome]
        ToriHere[isToriHere]
        MasterAsleep[isMasterAsleep]
        GuestAsleep[isGuestAsleep]
        TVPlaying[isTVPlaying]
        AlarmTime[alarmTime]
        BatteryPercent[sensor.span_battery_*]
        GuestDoor[isGuestBedroomDoorOpen]
        HaveGuests[isHaveGuests]
        Reset[reset]
        NickNearHome[isNickNearHome]
        CarolineNearHome[isCarolineNearHome]
        NickOffice[isNickOfficeOccupied]
        Kitchen[isKitchenOccupied]
        PrimaryDoor[isPrimaryBedroomDoorOpen]
        SexModeInput[input_boolean.sex]
    end

    subgraph "Computed State Variables"
        AnyOwnerHome[isAnyOwnerHome]
        AnyoneHome[isAnyoneHome]
        AnyoneAsleep[isAnyoneAsleep]
        EveryoneAsleep[isEveryoneAsleep]
        AnyoneHomeAndAwake[isAnyoneHomeAndAwake]
        DayPhase[dayPhase]
        SunEvent[sunevent]
        BatteryLevel[batteryEnergyLevel]
        SolarLevel[solarProductionEnergyLevel]
        CurrentEnergy[currentEnergyLevel]
        FreeEnergy[isFreeEnergyAvailable]
        OwnerJustReturned[didOwnerJustReturnHome]
    end

    subgraph "Output State Variables"
        MusicType[musicPlaybackType]
        MusicURI[currentlyPlayingMusicUri]
        FadeOut[isFadeOutInProgress]
        WakeActive[isWakeSequenceActive]
        Lockdown[isLockdown]
        AppleTVPlaying[isAppleTVPlaying]
        TVon[isTVon]
        GridAvailable[isGridAvailable]
        Expecting[isExpectingSomeone]
    end

    subgraph "Plugins"
        StateTracking[State Tracking Plugin<br/>Order: 10]
        DayPhasePlugin[Day Phase Plugin]
        Music[Music Plugin]
        Lighting[Lighting Plugin]
        Energy[Energy Plugin]
        SleepHygiene[Sleep Hygiene Plugin]
        TV[TV Plugin]
        Security[Security Plugin]
        SexModePlugin[Sex Mode Plugin<br/>Order: 65]
        LoadShedding[Load Shedding Plugin]
        ChristmasPlugin[Christmas Plugin]
        EnvironmentalPlugin[Environmental Plugin]
        SensorHealthPlugin[Sensor Health Plugin]
        WaterFlowPlugin[Water Flow Plugin<br/>Order: 71]
        EVChargerPlugin[EV Charger Safety Plugin<br/>Order: 5]
        ResetCoord[Reset Coordinator<br/>Order: 90]
    end

    subgraph "HA Entities (Sensors)"
        HumiditySensors[sensor.*_humidity]
        WeatherStationHumidity[sensor.weather_station_humidity]
        WaterLeakSensors[binary_sensor.*water_leak*]
        BatteryStateSensors[sensor.*_battery]
        NodeStatusSensors[sensor.*_node_status]
        WaterFlowSensor[sensor.droplet_flow_rate]
        EVChargerSensors[binary_sensor.leaf_charger_*<br/>sensor.leaf_charger_*<br/>switch.leaf_charger]
    end

    NickHome --> StateTracking
    CarolineHome --> StateTracking
    ToriHere --> StateTracking
    MasterAsleep --> StateTracking
    GuestAsleep --> StateTracking
    GuestDoor --> StateTracking
    HaveGuests --> StateTracking
    NickNearHome --> StateTracking
    CarolineNearHome --> StateTracking

    StateTracking --> AnyOwnerHome
    StateTracking --> AnyoneHome
    StateTracking --> AnyoneAsleep
    StateTracking --> EveryoneAsleep
    StateTracking --> OwnerJustReturned

    AnyoneHome --> AnyoneHomeAndAwake
    AnyoneAsleep --> AnyoneHomeAndAwake
    WakeActive -->|latch activation| AnyoneHomeAndAwake
    AnyoneAsleep -->|latch clearing| AnyoneHomeAndAwake

    DayPhasePlugin --> DayPhase
    DayPhasePlugin --> SunEvent

    AnyoneHome --> Music
    AnyoneAsleep --> Music
    DayPhase --> Music
    WakeActive --> Music
    AnyoneHomeAndAwake --> Music
    Music --> MusicType
    Music --> MusicURI

    DayPhase --> Lighting
    SunEvent --> Lighting
    AnyoneHome --> Lighting
    AnyoneAsleep --> Lighting
    AnyoneHomeAndAwake --> Lighting
    TVPlaying --> Lighting
    HaveGuests --> Lighting
    NickOffice --> Lighting
    Kitchen --> Lighting
    PrimaryDoor --> Lighting

    BatteryPercent --> Energy
    Energy --> BatteryLevel
    Energy --> SolarLevel
    Energy --> CurrentEnergy
    Energy --> FreeEnergy

    AlarmTime --> SleepHygiene
    MasterAsleep --> SleepHygiene
    SleepHygiene --> FadeOut
    SleepHygiene --> WakeActive
    SleepHygiene -.->|Triggers| Music
    SleepHygiene -.->|Triggers| Lighting

    TV --> AppleTVPlaying
    TV --> TVon
    TV --> TVPlaying

    AnyoneHome --> Security
    EveryoneAsleep --> Security
    OwnerJustReturned --> Security
    Expecting --> Security
    Security --> Lockdown

    SexModeInput --> SexModePlugin
    MusicType --> SexModePlugin
    DayPhase --> SexModePlugin
    MasterAsleep --> SexModePlugin
    AnyoneAsleep --> SexModePlugin
    WakeActive --> SexModePlugin
    SexModePlugin --> MusicType
    SexModePlugin --> WakeActive
    SexModePlugin -.->|Triggers| Lighting
    SexModePlugin -.->|Controls| EightSleep[Eight Sleep]

    CurrentEnergy --> LoadShedding
    AnyoneHome --> LoadShedding
    EveryoneAsleep --> LoadShedding

    DayPhase --> ChristmasPlugin
    AnyoneHome --> ChristmasPlugin

    HumiditySensors --> EnvironmentalPlugin
    WeatherStationHumidity -->|Outdoor Reference| EnvironmentalPlugin
    WaterLeakSensors --> EnvironmentalPlugin
    EnvironmentalPlugin -.->|Notifications| Ntfy[ntfy.sh]

    BatteryStateSensors --> SensorHealthPlugin
    NodeStatusSensors --> SensorHealthPlugin
    SensorHealthPlugin -.->|Notifications| Ntfy

    WaterFlowSensor --> WaterFlowPlugin
    WaterFlowPlugin -.->|Notifications| Ntfy

    EVChargerSensors --> EVChargerPlugin
    EVChargerPlugin -.->|Notifications| Ntfy

    Reset --> ResetCoord
    ResetCoord -.->|Reset| StateTracking
    ResetCoord -.->|Reset| DayPhasePlugin
    ResetCoord -.->|Reset| Energy
    ResetCoord -.->|Reset| LoadShedding
    ResetCoord -.->|Reset| Lighting
    ResetCoord -.->|Reset| Music
    ResetCoord -.->|Reset| Security
    ResetCoord -.->|Reset| SexModePlugin
    ResetCoord -.->|Reset| SleepHygiene
    ResetCoord -.->|Reset| ChristmasPlugin
    ResetCoord -.->|Reset| WaterFlowPlugin
    ResetCoord -.->|Reset| EVChargerPlugin

    style AnyOwnerHome fill:#fff3e0
    style AnyoneHome fill:#fff3e0
    style AnyoneAsleep fill:#fff3e0
    style EveryoneAsleep fill:#fff3e0
    style AnyoneHomeAndAwake fill:#fff3e0
    style DayPhase fill:#fff3e0
    style SunEvent fill:#fff3e0
    style BatteryLevel fill:#fff3e0
    style SolarLevel fill:#fff3e0
    style CurrentEnergy fill:#fff3e0
    style FreeEnergy fill:#fff3e0
    style OwnerJustReturned fill:#fff3e0

    style MusicType fill:#e8f5e9
    style MusicURI fill:#e8f5e9
    style FadeOut fill:#e8f5e9
    style Lockdown fill:#e8f5e9

    style ResetCoord fill:#ffebee
```

### State Variable Summary

| Category | Count | Examples |
|----------|-------|----------|
| **Boolean (input)** | 19 | isNickHome, isCarolineHome, isToriHere, isMasterAsleep, isHaveGuests, isNickOfficeOccupied, isKitchenOccupied, isPrimaryBedroomDoorOpen, isNickNearHome, isCarolineNearHome, isFrontOfHousePersonPresent |
| **Boolean (computed)** | 6 | isAnyOwnerHome, isAnyoneHome, isAnyoneAsleep, isEveryoneAsleep, isAnyoneHomeAndAwake, isGuestAsleep |
| **Boolean (output)** | 3 | isFadeOutInProgress, isWakeSequenceActive, isLockdown |
| **Number** | 3 | alarmTime, remainingSolarGeneration, thisHourSolarGeneration |
| **String (computed)** | 5 | dayPhase, sunevent, batteryEnergyLevel, currentEnergyLevel, solarProductionEnergyLevel |
| **String (output)** | 3 | musicPlaybackType, currentlyPlayingMusicUri, musicPlaylistRotation |
| **Local-only** | 2 | didOwnerJustReturnHome (bool), currentlyPlayingMusic (JSON) |

---

## How to Use These Diagrams

### Viewing in GitHub
All Mermaid diagrams render automatically in GitHub's markdown viewer.

### Viewing in VS Code
Install the "Markdown Preview Mermaid Support" extension for inline rendering.

### Updating Diagrams
When code changes significantly:
1. Update the relevant diagram(s) in this file
2. Ensure the diagram matches actual implementation
3. Reference file paths and line numbers when helpful
4. Update the "Last Updated" date in git commits

### Creating New Diagrams
Follow these conventions:
- Use consistent colors:
  - Light blue (`#e1f5ff`) for entry points
  - Light orange (`#fff3e0`) for decision/branching logic
  - Light green (`#e8f5e9`) for actions/outputs
  - Light purple (`#f3e5f5`) for shadow state / observability
  - Light red (`#ffebee`) for error/critical paths
- Include file references for traceability
- Keep diagrams focused on one concept/flow
- Link to implementation code with file paths

---

**Last Updated:** 2026-01-02
**Maintained By:** Development Team
**Related Documentation:**
- [ARCHITECTURE.md](../architecture/ARCHITECTURE.md) - Architecture and design decisions
- [migration_mapping.md](../reference/migration_mapping.md) - State variable mapping

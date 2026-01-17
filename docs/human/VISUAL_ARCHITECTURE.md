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
            Environmental[Environmental Monitoring<br/>internal/plugins/environmental/]
            ResetCoord[Reset Coordinator<br/>internal/plugins/reset/]
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
    Music -.->|Register Shadow| ShadowTracker

    Lighting -->|Get/Set State| StateManager
    Lighting -->|Call Services| HAClient
    Lighting -.->|Register Shadow| ShadowTracker

    Energy -->|Get/Set State| StateManager
    Energy -.->|Register Shadow| ShadowTracker

    TV -->|Get/Set State| StateManager

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
    Environmental -.->|Register Shadow| ShadowTracker

    ResetCoord -->|Subscribe to reset| StateManager
    ResetCoord -.->|Reset All| StateTracking
    ResetCoord -.->|Reset All| Music
    ResetCoord -.->|Reset All| Lighting

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
    style StateTracking fill:#f3e5f5
    style DayPhase fill:#f3e5f5
    style ResetCoord fill:#ffebee
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

    RecomputeDerived -->|Yes| Recompute[Recompute Derived Variables<br/>isAnyoneHomeAndAwake =<br/>isAnyOwnerHome AND !isAnyoneAsleep OR isToriHere]
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

    class LightingShadowState {
        +Plugin string
        +Inputs LightingInputs
        +Outputs LightingOutputs
        +Metadata StateMetadata
    }

    class MusicShadowState {
        +Plugin string
        +Inputs MusicInputs
        +Outputs MusicOutputs
        +Metadata StateMetadata
    }

    class SecurityShadowState {
        +Plugin string
        +Inputs SecurityInputs
        +Outputs SecurityOutputs
        +Metadata StateMetadata
    }

    class SexModeShadowState {
        +Plugin string
        +Inputs SexModeInputs
        +Outputs SexModeOutputs
        +Metadata StateMetadata
    }

    PluginShadowState <|.. LightingShadowState
    PluginShadowState <|.. MusicShadowState
    PluginShadowState <|.. SecurityShadowState
    PluginShadowState <|.. SexModeShadowState
    LightingShadowState --> StateMetadata
    MusicShadowState --> StateMetadata
    SecurityShadowState --> StateMetadata
    SexModeShadowState --> StateMetadata
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

Decision tree for music mode selection (matches Node-RED Music flow).

```mermaid
flowchart TD
    Start([State Change Detected]) --> GetState[Get Current State:<br/>isAnyoneHome<br/>isAnyoneAsleep<br/>dayPhase]

    GetState --> CheckHome{isAnyoneHome?}
    CheckHome -->|No| StopMusic[Set musicPlaybackType = '']
    CheckHome -->|Yes| CheckAsleep{isAnyoneAsleep?}

    CheckAsleep -->|Yes| SetSleep[Set musicPlaybackType = 'sleep']
    CheckAsleep -->|No| CheckDayPhase{dayPhase?}

    CheckDayPhase -->|morning| CheckWakeUp{Is Wake-Up Event?}
    CheckWakeUp -->|Yes| CheckSunday{Is Sunday?}
    CheckSunday -->|Yes| SetDay1[Set musicPlaybackType = 'day']
    CheckSunday -->|No| SetMorning[Set musicPlaybackType = 'morning']
    CheckWakeUp -->|No| SetDay2[Set musicPlaybackType = 'day']

    CheckDayPhase -->|day| SetDay3[Set musicPlaybackType = 'day']
    CheckDayPhase -->|sunset/dusk| SetEvening[Set musicPlaybackType = 'evening']
    CheckDayPhase -->|winddown/night| CheckCurrentSleep{Current Type<br/>= 'sleep'?}

    CheckCurrentSleep -->|Yes| KeepSleep[Keep musicPlaybackType = 'sleep']
    CheckCurrentSleep -->|No| SetWinddown[Set musicPlaybackType = 'winddown']

    StopMusic --> End1([End])
    SetSleep --> End2([End])
    SetMorning --> TriggerPlayback1
    SetDay1 --> TriggerPlayback2
    SetDay2 --> TriggerPlayback3
    SetDay3 --> TriggerPlayback4
    SetEvening --> TriggerPlayback5
    SetWinddown --> TriggerPlayback6
    KeepSleep --> End3([End])

    TriggerPlayback1[State Change Triggers<br/>Playback Handler] --> Orchestrate1
    TriggerPlayback2[State Change Triggers<br/>Playback Handler] --> Orchestrate2
    TriggerPlayback3[State Change Triggers<br/>Playback Handler] --> Orchestrate3
    TriggerPlayback4[State Change Triggers<br/>Playback Handler] --> Orchestrate4
    TriggerPlayback5[State Change Triggers<br/>Playback Handler] --> Orchestrate5
    TriggerPlayback6[State Change Triggers<br/>Playback Handler] --> Orchestrate6

    Orchestrate1[Orchestrate Playback] --> SelectPlaylist
    Orchestrate2[Orchestrate Playback] --> SelectPlaylist
    Orchestrate3[Orchestrate Playback] --> SelectPlaylist
    Orchestrate4[Orchestrate Playback] --> SelectPlaylist
    Orchestrate5[Orchestrate Playback] --> SelectPlaylist
    Orchestrate6[Orchestrate Playback] --> SelectPlaylist

    SelectPlaylist[Select Playlist with Rotation<br/>from music_config.yaml] --> BreakGroups[Break Existing Speaker Groups<br/>media_player.unjoin]
    BreakGroups --> BuildGroup[Build Sonos Speaker Group]
    BuildGroup --> MuteAll[Mute All Speakers to 0]
    MuteAll --> StartPlayback[Start Playback on Lead Player]
    StartPlayback --> EnableShuffle[Enable Shuffle for Playlists]
    EnableShuffle --> EvalConditions[Evaluate Mute Conditions<br/>for Each Speaker]
    EvalConditions --> FadeIn[Fade In Eligible Speakers<br/>Gradually 0→targetVolume]
    FadeIn --> UpdateShadow[Update Shadow State:<br/>mode, playlist, speakers]
    UpdateShadow --> Complete([Playback Complete])

    style Start fill:#e1f5ff
    style CheckHome fill:#fff3e0
    style CheckAsleep fill:#fff3e0
    style CheckDayPhase fill:#fff3e0
    style SelectPlaylist fill:#e8f5e9
    style BreakGroups fill:#e8f5e9
    style StartPlayback fill:#e8f5e9
    style UpdateShadow fill:#f3e5f5
```

**Reference:** See `homeautomation-go/internal/plugins/music/manager.go` for implementation details.

**Playback Sequence (matches Node-RED):**
1. Select playlist with rotation
2. Break existing speaker groups (media_player.unjoin)
3. Build new speaker group (media_player.join)
4. Mute all speakers to 0
5. Start playback on lead player
6. Enable shuffle for playlists
7. Fade in eligible speakers

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
        ResetCoord[Reset Coordinator<br/>Order: 90]
    end

    subgraph "HA Entities (Sensors)"
        HumiditySensors[sensor.*_humidity]
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

    DayPhasePlugin --> DayPhase
    DayPhasePlugin --> SunEvent

    AnyoneHome --> Music
    AnyoneAsleep --> Music
    DayPhase --> Music
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
    SexModePlugin --> MusicType
    SexModePlugin -.->|Triggers| Lighting
    SexModePlugin -.->|Controls| EightSleep[Eight Sleep]

    CurrentEnergy --> LoadShedding

    DayPhase --> ChristmasPlugin
    AnyoneHome --> ChristmasPlugin

    HumiditySensors --> EnvironmentalPlugin
    EnvironmentalPlugin -.->|Notifications| HAClient

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

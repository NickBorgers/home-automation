# TV Audio Output

The TV plugin selects the Bravia audio system once when playback first begins in
each TV-use session. It calls Home Assistant's `remote.send_command` service for
`remote.sony_xr_65a80k` with `AudioOutput_AudioSystem`, then repeats the command
after five seconds because the TV may not accept the first command while its
audio path is still becoming ready.

Pause/play changes do not repeat the pair. Turning the TV panel off resets the
session gate. Startup state initialization also does not manufacture a panel
power transition; a later real playback transition can select the audio system.
Failures are logged independently and do not change the computed TV power or
playback states.

The command is sent only after `isTVPlaying` becomes true. At that point the
music plugin's Soundbar `hands_off` rule prevents grouping, volume, mute, or
playback commands from competing with TV ownership.

## Deployment verification

After deployment, begin a new TV playback session and verify:

1. The TV plugin logs two successful audio-system selections about five seconds
   apart, with no additional pair during pause/resume changes.
2. Home Assistant's Bravia remote entity remains available and the TV reports
   or audibly uses its external audio system.
3. Music orchestration sends no ordinary Sonos command to the Soundbar while
   `isTVPlaying` is true.

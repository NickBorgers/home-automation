# Lux calibration readings, 2026-10-31 and 2026-11-01

Every reading from the handheld lux meter session, kept for future work. The final tables are in `configs/hue_config.yaml`. How they are used is in [LUX_LIGHTING.md](../flows/LUX_LIGHTING.md).

Times are local 24-hour. Local time was CDT until 2026-11-01 02:00, then CST.

## 1. Instrument and method

- Handheld lux meter, read at hand height, flat and facing up.
- The meter's ranges were quoted as "x100", so a reading of 303 x 100 is 30,300 lux.
- Outdoors the meter reads about 81% of `sensor.weather_station_illuminance`.
- Paired readings agreed within 1 to 2%.
- Ambient light in the dark was 0 to 2 lux.
- Daylight factor = indoor lux with the fixture off, divided by the station value at the same moment. The code multiplies the station value by the factor, so the 81% cancels out and the estimate is in the meter's units.

## 2. Outdoor baseline, 2026-10-31 at about 13:12 CDT

| Meter | Lux |
|-------|----:|
| Reading 1 | 30,300 |
| Reading 2 | 33,000 |
| Reading 3 | 32,400 |
| Mean | 31,900 |
| Station | 39,154 |

Ratio meter / station: 0.81.

## 3. Daylight-only indoor readings (fixture off), 2026-10-31

Sky: sun and thin cloud. Station 24,000 to 39,000 lux over 13:10 to 13:30.

| Place | Indoor lux | Station lux | Factor used |
|-------|-----------:|------------:|------------:|
| Living Room | 39.2, 39.7 (at 13:14) | 35,553 | 0.0011 (39.45 lux) |
| Kitchen island | 39.1, 39.6 | 32,135 | 0.0012 (39.35 lux) |
| Kitchen workspace (by the strip) | 25.5 (at 13:26) | 25,965 | 0.0010 |
| Front Room, daytime spot | 866, 883 | about 32,135 to 32,833 | 0.027 (874.5 lux) |
| Front Room, window farther south | 640 | 32,833 | 0.019 |
| Front Foyer | 213, 211 | 34,844 | 0.0061 (212 lux) |

Each factor comes from one sky condition. The Front Room night-use spot was not measured in daylight, so 0.027 is an upper bound for it.

## 4. Afternoon fixture-on readings (daylight still present)

These are totals. Daylight must be subtracted. They were noisier than the dark session, and the dark values supersede them.

| Fixture | Brightness | Total lux | Daylight at the time |
|---------|-----------:|----------:|---------------------:|
| Living Room cans | 25% | 41 | about 31 to 38 |
| | 50% | 64.25 | |
| | 100% | 153.5 | |
| Kitchen island cans | 25% | 46 | about 26 to 31 |
| | 50% | 91 | |
| | 100% | 266.5 | |
| Kitchen strip | 15% | 41 | about 25.5 to 29 |
| | 30% | 76 | |
| | 60% | 201 | |
| | 100% | 711 | |

The afternoon strip values ran 27 to 35% high against the dark ones, probably from meter position. Strip lux is very position-sensitive.

## 5. Dark session, 2026-11-01 at about 19:59 to 20:40 CST

Only the named fixture on. Lux at the spot.

| Brightness | Living Room cans (4000 K) | Kitchen cans at the island (4000 K) | Front Room chandelier | Front Foyer chandelier |
|-----------:|--------------------------:|------------------------------------:|----------------------:|-----------------------:|
| 5% | 0.7 | 1.3 | 0.8 | 2 |
| 10% | 1.65 | 3.1 | 3.9 | 4.4 |
| 15% | 3.0 | 5.9 | not measured | not measured |
| 25% | 7.9 | 15.8 | 23 | 12.9 |
| 50% | 30.1 | 61.1 | 99 | 48 |
| 100% | 119.0 | 237 | 155 | 111 |

Kitchen strip, at the workspace spot under the cabinet:

| Brightness | 5% | 10% | 15% | 30% | 60% | 100% |
|------------|---:|----:|----:|----:|----:|-----:|
| Lux | 2 | 5.6 | 11.3 | 36.8 | 127.5 | 500 |

Notes:

- The Front Room chandelier was measured at the night-use spot, not the daytime spot. That spot gets slightly less light from the fixture and is more representative of night use.
- The Living Room cans follow roughly lux = 0.012 x pct^2. The Kitchen cans follow roughly 0.024 x pct^2.
- The chandeliers do not follow a square law. The Front Room goes from 50% to 100% by only 1.6 times.
- The Kitchen is two lighting domains: the island (cans) is calibrated, and the workspace (strip) is kept bright.
- The strip warms as it dims. Its color temperature was only judged by eye: near 3000 K at 60%.

## 5b. Pantry cans, dark session, 2026-11-02 evening

Handheld lux meter, ambient 0 lux, only the Pantry cans on (8 cans, 4000 K).

| Brightness | 5% | 10% | 15% | 25% | 50% | 100% |
|-----------:|---:|----:|----:|----:|----:|-----:|
| Lux | 1 | 3 | 6 | 16 | 64 | 263 |

The Pantry cans follow roughly 0.026 x pct^2, close to the Kitchen cans.

## 6. Not measured, and next steps

- The Pantry strip (not measured; it copies the Kitchen mapping by user choice), and the Pantry `daylight_factor`, which is still the phone-app value 0.0034 and needs a daylight reading with the meter.
- A daylight reading at the Front Room night-use spot.
- Daylight ratios under overcast or low sun. All ratios here are from one sky condition (sun and thin cloud).
- A re-check after a few weeks.
- The strip's color temperature, with a real meter.
- `lux_scale` (cans 0.5, chandeliers 0.2) is unchanged and is the next decision.

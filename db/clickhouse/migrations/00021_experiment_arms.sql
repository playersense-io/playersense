-- An experiment's arm, as the SDK reports it (project.md §43, §44).
--
-- flag_assignments took a player's arm from the value their first exposure carried. That is right
-- for a flag with no experiment, and wrong for one: a player the test did not take in is served
-- the flag's usual answer, which is often the control arm's value, and would be counted in the
-- control arm without ever being in the test.
--
-- config-api now tells the SDK, per flag, the experiment and arm a player is in, and the SDK puts
-- them on the exposure as `experiment` and `arm`. The view takes `arm` when it is there and the
-- value when it is not, so an SDK that predates experiments is still counted by value, and the
-- dashboard maps an experiment's arms by id or by value.

-- +goose Up
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_flag_assignments SYNC;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_flag_assignments TO flag_assignments AS
SELECT
    e.project_seq                                           AS project_seq,
    e.environment_seq                                       AS environment_seq,
    e.props_string['flag']                                  AS flag,
    e.device_id                                             AS device_id,
    min(e.event_time)                                       AS first_exposed,
    argMinState(tuple(
        multiIf(mapContains(e.props_string, 'arm'),   e.props_string['arm'],
                mapContains(e.props_string, 'value'), e.props_string['value'],
                mapContains(e.props_int, 'value'),    toString(e.props_int['value']),
                mapContains(e.props_float, 'value'),  toString(e.props_float['value']),
                mapContains(e.props_bool, 'value'),   if(e.props_bool['value'] = 0, 'false', 'true'),
                ''),
        toUInt8(if(mapContains(e.props_bool, 'published'), e.props_bool['published'], 1)),
        toUInt32(greatest(e.props_int['config_version'], 0))
    ), e.event_time)                                        AS arm
FROM events_raw AS e
WHERE e.event_type = 'exposure' AND e.props_string['flag'] != ''
GROUP BY project_seq, environment_seq, flag, device_id
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS mv_flag_assignments SYNC;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW IF NOT EXISTS mv_flag_assignments TO flag_assignments AS
SELECT
    e.project_seq                                           AS project_seq,
    e.environment_seq                                       AS environment_seq,
    e.props_string['flag']                                  AS flag,
    e.device_id                                             AS device_id,
    min(e.event_time)                                       AS first_exposed,
    argMinState(tuple(
        multiIf(mapContains(e.props_string, 'value'), e.props_string['value'],
                mapContains(e.props_int, 'value'),    toString(e.props_int['value']),
                mapContains(e.props_float, 'value'),  toString(e.props_float['value']),
                mapContains(e.props_bool, 'value'),   if(e.props_bool['value'] = 0, 'false', 'true'),
                ''),
        toUInt8(if(mapContains(e.props_bool, 'published'), e.props_bool['published'], 1)),
        toUInt32(greatest(e.props_int['config_version'], 0))
    ), e.event_time)                                        AS arm
FROM events_raw AS e
WHERE e.event_type = 'exposure' AND e.props_string['flag'] != ''
GROUP BY project_seq, environment_seq, flag, device_id
-- +goose StatementEnd

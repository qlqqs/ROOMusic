package zones

import (
	"context"
	"database/sql"
	"fmt"
)

func loadGroupMembers(ctx context.Context, tx *sql.Tx, userID string, state *UserState) error {
	rows, err := tx.QueryContext(ctx, `SELECT g.leader_zone_id, g.device_id
        FROM playback_group_members g JOIN playback_zones z ON z.id=g.leader_zone_id AND z.user_id=g.user_id
        WHERE g.user_id=$1 ORDER BY g.leader_zone_id,g.ordinal`, userID)
	if err != nil {
		return fmt.Errorf("load playback groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var leader, member string
		if err := rows.Scan(&leader, &member); err != nil {
			return err
		}
		if state.GroupMembers == nil {
			state.GroupMembers = make(map[string][]string)
		}
		state.GroupMembers[leader] = append(state.GroupMembers[leader], member)
	}
	return rows.Err()
}

func (s *Store) changeGroupMember(ctx context.Context, userID, zoneID, deviceID string, add bool) error {
	if add {
		_, err := s.tx.ExecContext(ctx, `INSERT INTO playback_group_members(device_id,user_id,leader_zone_id,ordinal)
            SELECT $3,z.user_id,z.id,COUNT(m.device_id)+1 FROM playback_zones z
            LEFT JOIN playback_group_members m ON m.leader_zone_id=z.id
            WHERE z.user_id=$1 AND z.id=$2 GROUP BY z.id`, userID, zoneID, deviceID)
		return err
	}
	_, err := s.tx.ExecContext(ctx, `WITH removed AS (
        DELETE FROM playback_group_members WHERE user_id=$1 AND leader_zone_id=$2 AND device_id=$3 RETURNING ordinal
    ) UPDATE playback_group_members SET ordinal=ordinal-1 WHERE user_id=$1 AND leader_zone_id=$2 AND ordinal>(SELECT ordinal FROM removed)`, userID, zoneID, deviceID)
	return err
}

package check_code

import (
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_GetTrack 覆盖 distance 70~234 的所有取值（逐点测试），
// 并校验每次生成的轨迹切片长度始终落在 [15, 30] 区间内。
func Test_GetTrack(t *testing.T) {
	const (
		minDistance = 70  // 最小有效 distance
		maxDistance = 234 // 最大 distance
		minPoints   = 15  // 轨迹点数量下限
		maxPoints   = 30  // 轨迹点数量上限
		// 轨迹生成带有随机抖动，每个 distance 多次采样，确保“始终”落在区间内
		samplesPerDistance = 10
	)

	for distance := minDistance; distance <= maxDistance; distance++ {
		t.Run(fmt.Sprintf("distance=%d", distance), func(t *testing.T) {
			var trackData []TrackPoint

			for i := range samplesPerDistance {
				trackData = GetTrack(distance, 480)

				require.NotEmpty(t, trackData, "distance=%d 第%d次生成结果为空", distance, i+1)
				require.GreaterOrEqual(t, len(trackData), minPoints,
					"distance=%d 第%d次生成的点数 %d 少于 %d", distance, i+1, len(trackData), minPoints)
				require.LessOrEqual(t, len(trackData), maxPoints,
					"distance=%d 第%d次生成的点数 %d 多于 %d", distance, i+1, len(trackData), maxPoints)
			}

			// 直接序列化
			jsonData, err := json.Marshal(trackData)
			require.NoError(t, err)
			require.NotEmpty(t, jsonData)
		})
	}
}

// TestGetTrack_DistanceTooSmall distance 小于 70 时无法覆盖缺口，应当返回 nil。
func TestGetTrack_DistanceTooSmall(t *testing.T) {
	for _, distance := range []int{-10, 0, 1, 69} {
		require.Nil(t, GetTrack(distance, 480), "distance=%d 应返回 nil", distance)
		require.Nil(t, GetTrackByte(distance, 480), "distance=%d 应返回 nil", distance)
	}
}

// TestGetTrack_Shape 校验单个轨迹的形态：起点落在随机区间内、
// X 不回退且不超过 distance、Y 抖动有界、时间戳严格递增。
func TestGetTrack_Shape(t *testing.T) {
	const (
		distance = 120
		y        = 480
		minStart = 950 // 起点随机范围下限
		maxStart = 1036
	)

	track := GetTrack(distance, y)
	require.NotEmpty(t, track)

	start := track[0].X
	require.GreaterOrEqual(t, start, minStart)
	require.LessOrEqual(t, start, maxStart)

	var prevT int64
	for i, p := range track {
		require.GreaterOrEqual(t, p.Y, y, "第 %d 个点 Y 小于起点", i)
		require.LessOrEqual(t, p.Y, y+5, "第 %d 个点 Y 抖动超出 [0,5]", i)
		require.Greater(t, p.T, prevT, "第 %d 个点时间戳未严格递增", i)
		require.GreaterOrEqual(t, p.X, start, "第 %d 个点 X 回退到起点之前", i)
		require.LessOrEqual(t, p.X, start+distance+15, "第 %d 个点 X 超出 distance 范围", i)
		prevT = p.T
	}
}

// TestGetTrack_YOffset 校验 y 参数会整体平移轨迹的纵坐标。
func TestGetTrack_YOffset(t *testing.T) {
	const y = 300
	for _, p := range GetTrack(100, y) {
		require.GreaterOrEqual(t, p.Y, y)
		require.LessOrEqual(t, p.Y, y+5)
	}
}

// TestGetTrackByte 校验 JSON 编码后的字段名与可反序列化性。
func TestGetTrackByte(t *testing.T) {
	data := GetTrackByte(120, 480)
	require.NotEmpty(t, data)

	var points []TrackPoint
	require.NoError(t, json.Unmarshal(data, &points))
	require.NotEmpty(t, points)
	require.GreaterOrEqual(t, len(points), 15)
	require.LessOrEqual(t, len(points), 30)

	for i, p := range points {
		require.Positive(t, p.T, "第 %d 个点时间戳应为正", i)
		require.Positive(t, p.X, "第 %d 个点 X 应为正", i)
	}

	// 字段名必须与前端提交的 {x,y,t} 一致，多余/缺失都会导致 verify 失败。
	var raw []map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw, len(points))
	for i, p := range raw {
		require.Len(t, p, 3, "第 %d 个点字段数不对", i)
		for _, key := range []string{"x", "y", "t"} {
			require.Contains(t, p, key, "第 %d 个点缺少字段 %s", i, key)
		}
	}
}

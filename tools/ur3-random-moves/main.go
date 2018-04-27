// u3-random-moves generates a gcode file with random moves.
// The expected use is to move the UR3 in the front of a radar + camera
// to collect training data for Radar GAN.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"time"
)

var (
	totalDuration = flag.Duration("duration", time.Minute, "How long should the robot move")
	pauseDuration = flag.Duration("pause", time.Second, "How long to pause between move steps")
	stepDuration  = flag.Duration("stepDuration", time.Second, "How long to execute a step")
	stepLength    = flag.Float64("stepLength", 20, "Step length in mm")
)

// Matrix multiply for 3x3 matrices.
func matmul3(a, b [3][3]float64) (res [3][3]float64) {
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 0; k++ {
				res[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return
}

func rpy2RotVec(roll, pitch, yaw float64) ([3]float64, error) {
	log.Printf("roll=%v, pitch=%v, yaw=%v", roll, pitch, yaw)
	rollM := [3][3]float64{
		{1, 0, 0},
		{0, math.Cos(roll), -math.Sin(roll)},
		{0, math.Sin(roll), math.Cos(roll)},
	}
	pitchM := [3][3]float64{
		{math.Cos(pitch), 0, math.Sin(pitch)},
		{0, 1, 0},
		{-math.Sin(pitch), 0, math.Cos(pitch)},
	}
	yawM := [3][3]float64{
		{math.Cos(yaw), -math.Sin(yaw), 0},
		{math.Sin(yaw), math.Cos(yaw), 0},
		{0, 0, 1},
	}
	rotM := matmul3(matmul3(yawM, pitchM), rollM)
	log.Printf("rotM: %v", rotM)
	theta := math.Acos(((rotM[0][0] + rotM[1][1] + rotM[2][2]) - 1) / 2)
	var denom = 2 * math.Sin(theta)
	log.Printf("theta: %v, denom: %v", theta, denom)
	if math.Abs(denom) < 1E-3 {
		return [3]float64{0, 0, 0}, fmt.Errorf("anomaly point (denom is close to 0), can't move")
	}
	multi := 1.0 / denom
	return [3]float64{
		multi * (rotM[2][1] - rotM[1][2]) * theta,
		multi * (rotM[0][2] - rotM[2][0]) * theta,
		multi * (rotM[1][0] - rotM[0][1]) * theta,
	}, nil
}

func main() {
	flag.Parse()

	fmt.Println("; Home it first")
	fmt.Println("movej(get_inverse_kin(p[-0.32, -0.112468, 0.22599999999999998, 2.2193304157225078, 2.2215519673201243, 0.0000011102208116102493]), a=0.5, v=0.5)")
	fmt.Println("; host dwell")
	fmt.Println("M7821 P3000")

	span := *stepDuration + *pauseDuration
	for d := time.Duration(0); d < *totalDuration; d += span {
		fmt.Printf("; Step at %v\n", d)
		fmt.Printf("; host dwell\n")
		fmt.Printf("M7821 P%d\n\n", int(1000*span.Seconds()))
	}
}
